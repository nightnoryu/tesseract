package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nightnoryu/tesseract/internal/generated/api"
	"github.com/nightnoryu/tesseract/internal/platform/telemetry"
)

func TestGeneratedContract(t *testing.T) {
	server := httptest.NewServer(New(func(context.Context) error { return nil }, &capture{}, telemetry.New()))
	defer server.Close()
	client, err := api.NewClient(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	health, err := client.GetHealth(context.Background())
	if err != nil || health.Status != api.HealthStatusOk {
		t.Fatalf("health: %v %v", health, err)
	}
}

func TestContractErrors(t *testing.T) {
	logs := &capture{}
	server := New(func(context.Context) error { return nil }, logs, telemetry.New())
	for _, tc := range []struct {
		method, path, cookie string
		status               int
		code                 string
	}{
		{"GET", "/api/v1/catalog", "", 401, "unauthorized"},
		{"GET", "/api/v1/catalog", "tesseract_session=secret", 501, "not_implemented"},
		{"POST", "/api/v1/auth/login", "", 501, "not_implemented"},
		{"GET", "/api/v1/dashboards/not-a-uuid/shares", "tesseract_session=secret", 501, "not_implemented"},
		{"GET", "/api/v1/unknown-secret", "", 404, "not_found"},
		{"DELETE", "/healthz", "", 405, "method_not_allowed"},
	} {
		t.Run(tc.method+tc.path+tc.cookie, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.path, nil)
			request.Header.Set("Cookie", tc.cookie)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			var body api.ErrorEnvelope
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != tc.status || body.Error.Code != tc.code || body.RequestID == "" || body.RequestID != response.Header().Get("X-Request-ID") {
				t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "secret") {
				t.Fatal("error leaked request content")
			}
		})
	}
	for _, fields := range logs.fields {
		if strings.Contains(fields["route"].(string), "not-a-uuid") || strings.Contains(fields["route"].(string), "secret") {
			t.Fatalf("unbounded route: %v", fields)
		}
	}
}
