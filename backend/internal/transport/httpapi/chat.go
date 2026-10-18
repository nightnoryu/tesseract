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
	result, err := h.catalog.Chat.Send(ctx, session.ID, id, req.Message)
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
