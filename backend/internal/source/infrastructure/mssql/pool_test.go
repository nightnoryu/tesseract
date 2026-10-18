package mssql

import (
	"context"
	"errors"
	"testing"
)

func TestConfigureOfflinePool(t *testing.T) {
	// A non-routable endpoint must not be contacted during API configuration.
	c, err := New(testCatalog(t), "crm_dwh", "sqlserver://reader:unused@192.0.2.1:1433?database=warehouse", DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.InspectCatalogTables(ctx, []string{"leads"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("offline request ignored cancellation: %v", err)
	}
}
