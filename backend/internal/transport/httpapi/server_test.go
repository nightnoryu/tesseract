package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nightnoryu/go-kita/log"
	"github.com/nightnoryu/tesseract/internal/platform/telemetry"
)

type capture struct{ fields []log.Fields }

func (c *capture) WithFields(f log.Fields) log.Logger { c.fields = append(c.fields, f); return c }
func (*capture) Debug(...any)                         {}
func (*capture) Info(...any)                          {}
func (*capture) Warn(...any)                          {}
func (*capture) Error(error, ...any)                  {}

func TestReadinessAndDrain(t *testing.T) {
	var unavailable bool
	calls := 0
	s := New(func(ctx context.Context) error {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Error("probe has no deadline")
		}
		if unavailable {
			return errors.New("secret connection details")
		}
		return nil
	}, &capture{}, telemetry.New())
	for _, tc := range []struct {
		path        string
		down, drain bool
		status      int
		body        string
	}{
		{"/readyz", false, false, 200, "available"},
		{"/readyz", true, false, 503, "database_unavailable"},
		{"/healthz", true, false, 200, "ok"},
		{"/readyz", false, true, 503, "shutting_down"},
	} {
		unavailable = tc.down
		if tc.drain {
			s.Drain()
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.body) || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	if calls != 2 {
		t.Fatalf("unexpected database probes: %d", calls)
	}
}

func TestRequestPrivacyAndBoundedLabels(t *testing.T) {
	logs := &capture{}
	m := telemetry.New()
	s := New(func(context.Context) error { return nil }, logs, m)
	for _, path := range []string{"/private-user-123?token=secret", "/another-user-456", "/healthz?password=secret"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("X-Request-ID", "attacker-controlled-secret")
		r.Header.Set("Authorization", "Bearer secret")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if id := w.Header().Get("X-Request-ID"); id == "" || strings.Contains(id, "secret") {
			t.Fatal("unsafe correlation ID")
		}
	}
	for _, fields := range logs.fields {
		for _, value := range fields {
			if s, ok := value.(string); ok && (strings.Contains(s, "secret") || strings.Contains(s, "user-")) {
				t.Fatalf("private value logged: %v", fields)
			}
		}
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	body := w.Body.String()
	for _, want := range []string{`route="unmatched",status_group="4xx"} 2`, "go_goroutines", "process_cpu_seconds_total", "tesseract_llm_requests_total", "tesseract_llm_errors_total", "tesseract_llm_request_duration_seconds"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing metric %s", want)
		}
	}
	if strings.Contains(body, "private-user-123") || strings.Contains(body, "another-user-456") || strings.Contains(body, "secret") {
		t.Fatal("private metric label")
	}
}

func TestPanicIsSanitized(t *testing.T) {
	logs := &capture{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /panic", func(w http.ResponseWriter, r *http.Request) {
		if RequestID(r.Context()) == "" {
			t.Error("missing context ID")
		}
		panic("secret")
	})
	w := httptest.NewRecorder()
	observe(mux, logs, telemetry.New()).ServeHTTP(w, httptest.NewRequest("GET", "/panic", nil))
	if w.Code != 500 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("unsafe panic response: %s", w.Body.String())
	}
	if logs.fields[0]["error_code"] != "internal_error" {
		t.Fatal("missing safe error code")
	}
}
