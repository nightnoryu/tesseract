package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/nightnoryu/tesseract/internal/generated/api"
	ogenhttp "github.com/ogen-go/ogen/http"
	"github.com/ogen-go/ogen/ogenerrors"
)

type contract struct {
	api.UnimplementedHandler
	catalog *CatalogConfig
	server  *Server
	ping    func(context.Context) error
}

func envelope(ctx context.Context, code, message string) api.ErrorEnvelope {
	return api.ErrorEnvelope{Error: api.ErrorEnvelopeError{Code: code, Message: message}, RequestID: RequestID(ctx)}
}

func (h *contract) GetHealth(context.Context) (*api.Health, error) {
	return &api.Health{Status: api.HealthStatusOk}, nil
}

func (h *contract) GetReadiness(ctx context.Context) (api.GetReadinessRes, error) {
	result := api.Readiness{Status: "ready", Postgresql: "available", Dwh: "not_configured"}
	if h.server.draining.Load() {
		result.Status, result.Postgresql = "shutting_down", "not_checked"
	} else {
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if h.ping(ctx) != nil {
			result.Status, result.Postgresql = "database_unavailable", "unavailable"
		}
	}
	if result.Status != "ready" {
		return (*api.GetReadinessServiceUnavailable)(&result), nil
	}
	return (*api.GetReadinessOK)(&result), nil
}

func (h *contract) HandleSessionCookie(ctx context.Context, _ api.OperationName, _ api.SessionCookie) (context.Context, error) {
	// No cookie is trusted until session verification is implemented.
	return ctx, ogenhttp.ErrNotImplemented
}

func (h *contract) NewError(ctx context.Context, err error) *api.ErrorStatusCode {
	status, code, message := 500, "internal_error", "An internal error occurred."
	var security *ogenerrors.SecurityError
	if errors.Is(err, ogenhttp.ErrNotImplemented) {
		status, code, message = 501, "not_implemented", "This feature is not available yet."
	} else if errors.As(err, &security) {
		status, code, message = 401, "unauthorized", "Sign-in is required."
	}
	return &api.ErrorStatusCode{StatusCode: status, Response: envelope(ctx, code, message)}
}

func newContractServer(h *contract) *api.Server {
	server, err := api.NewServer(h, h,
		api.WithNotFound(func(w http.ResponseWriter, r *http.Request) {
			respond(w, 404, envelope(r.Context(), "not_found", "Route not found."))
		}),
		api.WithMethodNotAllowed(func(w http.ResponseWriter, r *http.Request, allowed string) {
			w.Header().Set("Allow", allowed)
			respond(w, 405, envelope(r.Context(), "method_not_allowed", "Method not allowed."))
		}),
		api.WithErrorHandler(func(ctx context.Context, w http.ResponseWriter, r *http.Request, err error) {
			status := ogenerrors.ErrorCode(err)
			if status >= 500 || status == 401 {
				response := h.NewError(ctx, err)
				respond(w, response.StatusCode, response.Response)
				return
			}
			respond(w, status, envelope(ctx, "invalid_request", "The request is invalid."))
		}),
	)
	if err != nil {
		panic("invalid API server configuration")
	}
	return server
}
