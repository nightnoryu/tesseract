package httpapi

import (
	"context"
	"encoding/json"
	auth "github.com/nightnoryu/tesseract/internal/auth/domain"
	"github.com/nightnoryu/tesseract/internal/chat"
	"github.com/nightnoryu/tesseract/internal/generated/api"
)

func (h *contract) SendChatMessage(ctx context.Context, req *api.ChatRequest) (*api.ChatResponse, error) {
	session, ok := ctx.Value(sessionKey{}).(auth.Session)
	if !ok {
		return nil, auth.ErrCredentials
	}
	if h.catalog == nil || h.catalog.Chat == nil {
		return nil, chat.ErrUnavailable
	}
	id := ""
	if v, ok := req.SessionID.Get(); ok {
		id = v.String()
	}
	var result chat.Result
	var err error
	if dashboardID, editing := req.DashboardID.Get(); editing {
		expected, present := req.ExpectedRevision.Get()
		if !present || id != "" {
			return nil, chat.ErrInvalid
		}
		result, err = h.catalog.Chat.SendEdit(ctx, session.ID, dashboardID.String(), expected, req.Message)
	} else {
		if req.ExpectedRevision.IsSet() {
			return nil, chat.ErrInvalid
		}
		result, err = h.catalog.Chat.Send(ctx, session.ID, id, req.Message)
	}
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var out api.ChatResponse
	if err = out.UnmarshalJSON(raw); err != nil {
		return nil, err
	}
	return &out, nil
}

func (h *contract) ListDashboardChat(ctx context.Context, p api.ListDashboardChatParams) ([]api.DashboardChatTurn, error) {
	s, user, err := h.dashboards(ctx)
	if err != nil {
		return nil, err
	}
	turns, err := s.Turns(ctx, user, p.DashboardId.String(), p.Limit.Or(10), p.Offset.Or(0))
	if err != nil {
		return nil, err
	}
	out := make([]api.DashboardChatTurn, 0, len(turns))
	for _, turn := range turns {
		raw, err := json.Marshal(turn)
		if err != nil {
			return nil, err
		}
		var value api.DashboardChatTurn
		if err = value.UnmarshalJSON(raw); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}
func (h *contract) DiscardDashboardDraft(ctx context.Context, req *api.PublishDashboard, p api.DiscardDashboardDraftParams) (*api.Dashboard, error) {
	s, user, err := h.dashboards(ctx)
	if err != nil {
		return nil, err
	}
	return dashboardResponse(s.Discard(ctx, user, p.DashboardId.String(), req.ExpectedRevision))
}
