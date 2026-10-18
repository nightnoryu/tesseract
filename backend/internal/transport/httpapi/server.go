package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/nightnoryu/go-kita/log"
	"github.com/nightnoryu/tesseract/internal/generated/api"
	"github.com/nightnoryu/tesseract/internal/platform/telemetry"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type requestIDKey struct{}

func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

type Server struct {
	draining atomic.Bool
	handler  http.Handler
}

func New(ping func(context.Context) error, logger log.Logger, metrics *telemetry.Metrics, catalog ...CatalogConfig) *Server {
	s := &Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("Tesseract development server\n")) })
	h := &contract{server: s, ping: ping}
	if len(catalog) > 0 {
		h.catalog = &catalog[0]
		h.auth = catalog[0].Auth
	}
	generated := newContractServer(h)
	mux.Handle("/", authBoundary(generated, h.auth))
	mux.Handle("GET /metrics", promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{ErrorHandling: promhttp.HTTPErrorOnError}))
	s.handler = observe(mux, logger, metrics, generated)
	return s
}

func (s *Server) Drain()                                           { s.draining.Store(true) }
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

func respond(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

type response struct {
	http.ResponseWriter
	status int
}

func (w *response) WriteHeader(status int) {
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *response) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}
func (w *response) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func observe(mux *http.ServeMux, logger log.Logger, metrics *telemetry.Metrics, generated ...*api.Server) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := rand.Text()
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))
		w.Header().Set("X-Request-ID", id)
		if r.URL.Path == "/api/v1/catalog" {
			w.Header().Set("Cache-Control", "no-store")
		}
		out := &response{ResponseWriter: w}
		metrics.InFlight.Inc()
		// ServeMux may return a redirect path as its pattern. Admit only registered templates.
		_, route := mux.Handler(r)
		switch route {
		case "GET /{$}", "GET /healthz", "GET /readyz", "GET /metrics":
		default:
			route = "unmatched"
		}
		if len(generated) > 0 && route == "unmatched" {
			if matched, ok := generated[0].FindRoute(r.Method, r.URL.Path); ok {
				route = r.Method + " " + matched.PathPattern()
			}
		}
		defer func() {
			code := ""
			abort := false
			if recover() != nil {
				code = "internal_error"
				if out.status == 0 {
					respond(out, http.StatusInternalServerError, envelope(r.Context(), code, "An internal error occurred."))
				} else {
					abort = true
				}
			}
			if out.status == 0 {
				out.status = http.StatusOK
			}
			if code == "" && out.status >= 400 {
				code = fmt.Sprintf("http_%dxx", out.status/100)
			}
			elapsed := time.Since(start)
			metrics.InFlight.Dec()
			metrics.Requests.WithLabelValues(route, fmt.Sprintf("%dxx", out.status/100)).Inc()
			metrics.Duration.WithLabelValues(route).Observe(elapsed.Seconds())
			logger.WithFields(log.Fields{"request_id": id, "route": route, "status": out.status, "duration_ms": float64(elapsed) / float64(time.Millisecond), "error_code": code}).Info("http_request")
			if abort {
				panic(http.ErrAbortHandler)
			}
		}()
		mux.ServeHTTP(out, r)
	})
}
