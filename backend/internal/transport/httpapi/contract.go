package httpapi

import (
	"context"
	"errors"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
	"net/http"
	"time"

	"github.com/nightnoryu/tesseract/internal/auth/domain"
	"github.com/nightnoryu/tesseract/internal/generated/api"
	ogenhttp "github.com/ogen-go/ogen/http"
	"github.com/ogen-go/ogen/ogenerrors"
)

type contract struct {
	api.UnimplementedHandler
	auth    *AuthConfig
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

func (h *contract) NewError(ctx context.Context, err error) *api.ErrorStatusCode {
	status, code, message := 500, "internal_error", "An internal error occurred."
	var security *ogenerrors.SecurityError
	if errors.As(err, &security) {
		status, code, message = 401, "unauthorized", "Sign-in is required."
	} else if errors.Is(err, domain.ErrCredentials) {
		status, code, message = 401, "invalid_credentials", "Email or password is incorrect."
	} else if errors.Is(err, domain.ErrLimited) {
		status, code, message = 429, "rate_limited", "Too many attempts. Try again later."
	} else if errors.Is(err, domain.ErrPassword) {
		status, code, message = 400, "invalid_password", "Use a password of at least 12 characters and at most 1024 bytes."
	} else if errors.Is(err, source.ErrAccessDenied) {
		status, code, message = 403, "access_denied", "Data access is not permitted."
	} else if errors.Is(err, source.ErrInvalidQuery) || errors.Is(err, source.ErrLimit) {
		status, code, message = 400, "invalid_query", "The query is invalid."
	} else if errors.Is(err, source.ErrSchemaUnavailable) || errors.Is(err, source.ErrUnavailable) || errors.Is(err, source.ErrReadOnly) {
		status, code, message = 503, "data_unavailable", "The data source is unavailable or unverified."
	} else if errors.Is(err, context.DeadlineExceeded) {
		status, code, message = 504, "query_timeout", "The query timed out."
	} else if errors.Is(err, ogenhttp.ErrNotImplemented) {
		status, code, message = 501, "not_implemented", "This feature is not available yet."
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
