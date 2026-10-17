package mssql

import (
	"context"
	"errors"
	semantic "github.com/nightnoryu/tesseract/internal/semantic/domain"
	"github.com/nightnoryu/tesseract/internal/source/domain"
	"testing"
)

func TestRejectBeforeDatabaseAccess(t *testing.T) {
	c := &Connector{datasets: map[string]semantic.Dataset{"leads": {ID: "leads"}}, cfg: DefaultConfig()}
	for _, id := range []string{"mart.MarketingLeads", "unlisted", "leads; DROP TABLE x", ""} {
		if _, err := c.InspectCatalogTables(context.Background(), []string{"leads", id}); !errors.Is(err, domain.ErrNotCataloged) {
			t.Fatalf("%q: %v", id, err)
		}
	}
	if _, err := c.ExecuteQuery(context.Background(), domain.ValidatedQuery{}); !errors.Is(err, domain.ErrInvalidQuery) {
		t.Fatal(err)
	}
}
func TestLimitsAndCancellation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Concurrency = cfg.MaxOpen + 1
	if cfg.Validate() == nil {
		t.Fatal("accepted excessive concurrency")
	}
	c := &Connector{slots: make(chan struct{}, 1)}
	c.slots <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(c.acquire(ctx), context.Canceled) {
		t.Fatal("queue ignored cancellation")
	}
}
