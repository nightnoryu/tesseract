package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	auth "github.com/nightnoryu/tesseract/internal/auth/domain"
	dashboard "github.com/nightnoryu/tesseract/internal/dashboard/app"
	"github.com/nightnoryu/tesseract/internal/dashboard/domain"
	"github.com/nightnoryu/tesseract/internal/generated/api"
)

func (h *contract) dashboards(ctx context.Context) (*dashboard.Service, string, error) {
	session, ok := ctx.Value(sessionKey{}).(auth.Session)
	if !ok {
		return nil, "", auth.ErrCredentials
	}
	if h.catalog == nil || h.catalog.Dashboards == nil {
		return nil, "", errors.New("dashboards_unavailable")
	}
	return h.catalog.Dashboards, session.ID, nil
}
func dashboardSpec(req *api.DashboardSpec) (domain.Spec, error) {
	raw, err := req.MarshalJSON()
	if err != nil {
		return domain.Spec{}, domain.ErrInvalid
	}
	var spec domain.Spec
	if err = json.Unmarshal(raw, &spec); err != nil {
		return spec, domain.ErrInvalid
	}
	return spec, nil
}
func dashboardResponse(d domain.Dashboard, err error) (*api.Dashboard, error) {
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	var out api.Dashboard
	err = out.UnmarshalJSON(raw)
	return &out, err
}
func (h *contract) CreateDashboard(ctx context.Context, req *api.DashboardSpec) (*api.Dashboard, error) {
	s, user, err := h.dashboards(ctx)
	if err != nil {
		return nil, err
	}
	spec, err := dashboardSpec(req)
	if err != nil {
		return nil, err
	}
	return dashboardResponse(s.Create(ctx, user, spec))
}
func (h *contract) GetDashboard(ctx context.Context, p api.GetDashboardParams) (*api.Dashboard, error) {
	s, user, err := h.dashboards(ctx)
	if err != nil {
		return nil, err
	}
	return dashboardResponse(s.Read(ctx, user, p.DashboardId.String()))
}
func (h *contract) SaveDashboardDraft(ctx context.Context, req *api.SaveDashboardDraft, p api.SaveDashboardDraftParams) (*api.Dashboard, error) {
	s, user, err := h.dashboards(ctx)
	if err != nil {
		return nil, err
	}
	spec, err := dashboardSpec(&req.Spec)
	if err != nil {
		return nil, err
	}
	return dashboardResponse(s.Save(ctx, user, p.DashboardId.String(), req.ExpectedRevision, spec, false))
}
func (h *contract) PublishDashboard(ctx context.Context, req *api.PublishDashboard, p api.PublishDashboardParams) (*api.Dashboard, error) {
	s, user, err := h.dashboards(ctx)
	if err != nil {
		return nil, err
	}
	return dashboardResponse(s.Save(ctx, user, p.DashboardId.String(), req.ExpectedRevision, domain.Spec{}, true))
}
func (h *contract) DeleteDashboard(ctx context.Context, p api.DeleteDashboardParams) error {
	s, user, err := h.dashboards(ctx)
	if err != nil {
		return err
	}
	return s.Delete(ctx, user, p.DashboardId.String(), p.ExpectedRevision)
}
func (h *contract) ListDashboards(ctx context.Context, p api.ListDashboardsParams) ([]api.Dashboard, error) {
	s, user, err := h.dashboards(ctx)
	if err != nil {
		return nil, err
	}
	ds, err := s.List(ctx, user, p.Limit.Or(10), p.Offset.Or(0))
	if err != nil {
		return nil, err
	}
	out := make([]api.Dashboard, 0, len(ds))
	for _, d := range ds {
		v, err := dashboardResponse(d, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, nil
}
func (h *contract) ListDashboardRevisions(ctx context.Context, p api.ListDashboardRevisionsParams) ([]api.DashboardRevision, error) {
	s, user, err := h.dashboards(ctx)
	if err != nil {
		return nil, err
	}
	rs, err := s.History(ctx, user, p.DashboardId.String(), p.Limit.Or(10), p.Offset.Or(0))
	if err != nil {
		return nil, err
	}
	out := make([]api.DashboardRevision, 0, len(rs))
	for _, r := range rs {
		raw, err := json.Marshal(r)
		if err != nil {
			return nil, err
		}
		var v api.DashboardRevision
		if err = v.UnmarshalJSON(raw); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
