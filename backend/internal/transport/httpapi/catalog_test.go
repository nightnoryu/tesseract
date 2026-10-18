package httpapi

import (
	"context"
	metadata "github.com/nightnoryu/tesseract/internal/metadata/app"
	"github.com/nightnoryu/tesseract/internal/metadata/domain"
	"github.com/nightnoryu/tesseract/internal/platform/telemetry"
	catalog "github.com/nightnoryu/tesseract/internal/semantic/infrastructure"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type catalogReader struct{ reads int }

func (r *catalogReader) Latest(context.Context, string, string) (domain.Snapshot, error) {
	r.reads++
	return domain.Snapshot{}, nil
}
func TestCatalogAuthentication(t *testing.T) {
	cat, err := catalog.Load(os.DirFS("../../../../catalog"))
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 32)
	for _, tc := range []struct {
		name, configured, supplied string
		status                     int
	}{
		{"valid", token, token, 200}, {"absent", token, "", 401}, {"invalid", token, "wrong", 401}, {"disabled", "", token, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &catalogReader{}
			server := New(func(context.Context) error { return nil }, &capture{}, telemetry.New(), CatalogConfig{OperatorToken: tc.configured, Availability: metadata.Availability{Catalog: cat, Store: reader, MaxAge: time.Minute}})
			req := httptest.NewRequest("GET", "/api/v1/catalog", nil)
			if tc.supplied != "" {
				req.Header.Set("Authorization", "Bearer "+tc.supplied)
			}
			res := httptest.NewRecorder()
			server.ServeHTTP(res, req)
			if res.Code != tc.status {
				t.Fatalf("%d %s", res.Code, res.Body.String())
			}
			if (reader.reads > 0) != (tc.status == 200) {
				t.Fatal("unauthenticated metadata read")
			}
			if tc.status == 200 && !strings.Contains(res.Body.String(), "not_scanned") {
				t.Fatal(res.Body.String())
			}
			if res.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("metadata response is cacheable")
			}
		})
	}
}
