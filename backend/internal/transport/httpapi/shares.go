package httpapi

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/nightnoryu/tesseract/internal/dashboard/domain"
	"github.com/nightnoryu/tesseract/internal/generated/api"
	"time"
)

func (h *contract) ListDashboardShares(ctx context.Context, p api.ListDashboardSharesParams) ([]api.DashboardShare, error) {
	s, user, err := h.dashboards(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	shares, err := s.Store.Shares(ctx, user, p.DashboardId.String())
	if err != nil {
		return nil, err
	}
	out := make([]api.DashboardShare, 0, len(shares))
	for _, v := range shares {
		out = append(out, api.DashboardShare{UserID: uuid.MustParse(v.UserID), Email: v.Email})
	}
	return out, nil
}
func (h *contract) GrantDashboardShare(ctx context.Context, req *api.GrantDashboardShareReq, p api.GrantDashboardShareParams) error {
	s, user, err := h.dashboards(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.Store.Grant(ctx, user, p.DashboardId.String(), req.Email)
}
func (h *contract) RevokeDashboardShare(ctx context.Context, p api.RevokeDashboardShareParams) error {
	s, user, err := h.dashboards(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.Store.Revoke(ctx, user, p.DashboardId.String(), p.UserId.String())
}
func (h *contract) QueryDashboardWidget(ctx context.Context, req []api.DashboardFilter, p api.QueryDashboardWidgetParams) (*api.QueryResult, error) {
	s, user, err := h.dashboards(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var filters []domain.Filter
	if err = json.Unmarshal(raw, &filters); err != nil {
		return nil, err
	}
	result, err := s.QueryWidget(ctx, user, p.DashboardId.String(), p.WidgetId, p.ExpectedRevision, filters)
	if err != nil {
		return nil, err
	}
	if result.Rows == nil {
		result.Rows = [][]any{}
	}
	raw, err = json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var out api.QueryResult
	err = out.UnmarshalJSON(raw)
	return &out, err
}
