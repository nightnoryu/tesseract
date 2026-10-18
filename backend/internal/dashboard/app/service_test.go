package app

import (
	"context"
	"errors"
	"fmt"
	access "github.com/nightnoryu/tesseract/internal/access/app"
	analytics "github.com/nightnoryu/tesseract/internal/analytics/app"
	"github.com/nightnoryu/tesseract/internal/dashboard/domain"
	metadata "github.com/nightnoryu/tesseract/internal/metadata/domain"
	catalog "github.com/nightnoryu/tesseract/internal/semantic/infrastructure"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
	"os"
	"strings"
	"testing"
	"time"
)

type snapshot struct{ reason string }

func (s *snapshot) Latest(context.Context, string, string) (metadata.Snapshot, error) {
	now := time.Now()
	return metadata.Snapshot{LastScan: now, Tables: []metadata.Table{{Dataset: "leads", Schema: "mart", Name: "MarketingLeads", Present: true, InspectedAt: now, Reason: s.reason}}}, nil
}
func TestValidationAndDrift(t *testing.T) {
	cat, err := catalog.Load(os.DirFS("../../../../catalog"))
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := cat.Hash()
	user := "00000000-0000-0000-0000-000000000001"
	policy, err := access.Load(strings.NewReader(fmt.Sprintf(`{"version":1,"catalog_hash":%q,"grants":[{"user_id":%q,"source":"crm_dwh","dataset":"leads","fields":["lead_count","channel"],"row_filters":[{"dimension":"channel","operator":"eq","values":[{"type":"string","value":"paid"}]}]}]}`, hash, user)), cat)
	if err != nil {
		t.Fatal(err)
	}
	snap := &snapshot{}
	s := Service{Engine: analytics.Engine{Catalog: cat, Store: snap, MaxAge: time.Minute}, Policy: policy}
	spec := domain.Spec{Version: 1, Title: "Leads", Filters: []domain.Filter{{ID: "channel", Operator: "eq", Values: []source.Value{{Type: "string", Value: "paid"}}}}, Widgets: []domain.Widget{{ID: "count", Title: "Count", Kind: "kpi", Layout: domain.Layout{Width: 3, Height: 2}, Query: source.QuerySpec{Dataset: "leads", Metrics: []string{"lead_count"}, Limit: 1}, FilterMappings: []domain.Mapping{{Filter: "channel", Dimension: "channel"}}}}}
	ctx := context.Background()
	if err := s.validate(ctx, user, spec); err != nil {
		t.Fatal(err)
	} // No connector: validation must not execute DWH queries.
	if err := s.validate(ctx, "other", spec); !errors.Is(err, source.ErrAccessDenied) {
		t.Fatal(err)
	}
	snap.reason = "incompatible_type"
	if err := s.validate(ctx, user, spec); !errors.Is(err, source.ErrSchemaUnavailable) {
		t.Fatal(err)
	}
	states, err := s.states(ctx, user, spec)
	if err != nil || states[0].Status != "unavailable" {
		t.Fatal(states, err)
	}
	snap.reason = ""
	spec.Filters[0].Values[0].Type = "integer"
	if err := s.validate(ctx, user, spec); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("mapped filter type accepted", err)
	}
	states, err = s.states(ctx, user, spec)
	if err != nil || states[0].Status != "invalid" {
		t.Fatal(states, err)
	}
	spec.Filters[0].Values[0].Type = "string"
	spec.Widgets[0].Query.Metrics = []string{"invented"}
	if err := s.validate(ctx, user, spec); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal(err)
	}
}
