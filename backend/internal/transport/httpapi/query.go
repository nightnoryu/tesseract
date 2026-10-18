package httpapi

import (
	"bytes"
	"context"
	"encoding/json"

	auth "github.com/nightnoryu/tesseract/internal/auth/domain"
	"github.com/nightnoryu/tesseract/internal/generated/api"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
)

func (h *contract) ExecuteQuery(ctx context.Context, req *api.QuerySpec) (*api.QueryResult, error) {
	session, ok := ctx.Value(sessionKey{}).(auth.Session)
	if !ok || h.catalog == nil {
		return nil, source.ErrAccessDenied
	}
	raw, err := req.MarshalJSON()
	if err != nil {
		return nil, source.ErrInvalidQuery
	}
	spec, err := source.DecodeQuery(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	result, err := h.catalog.Policy.Execute(ctx, h.catalog.Engine, session.ID, spec)
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
	if err := out.UnmarshalJSON(raw); err != nil {
		return nil, err
	}
	return &out, nil
}
