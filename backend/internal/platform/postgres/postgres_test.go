package postgres

import "testing"

func TestParseDSNRejectsLostOptionsAndSecrets(t *testing.T) {
	for _, raw := range []string{"", "postgres://user:secret@host/db?sslmode=disable", "postgres://user:secret@host/db#secret", "postgres://user:secret@host/", "postgres://user:secret@host:99999/db", "postgres://user:secret@host/db/extra"} {
		if _, err := ParseDSN(raw); err == nil || err.Error() != "invalid_database_configuration" {
			t.Fatalf("expected safe configuration error, got %v", err)
		}
	}
	dsn, err := ParseDSN("postgres://user:p%40ss@localhost:5433/app")
	if err != nil || dsn.Password != "p@ss" || dsn.Port != 5433 || dsn.Database != "app" {
		t.Fatal("valid connection rejected")
	}
}
