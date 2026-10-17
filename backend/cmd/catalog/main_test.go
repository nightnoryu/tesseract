package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestValidateCommand(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		code int
	}{
		{"valid", []string{"validate", "-dir", "../../../catalog"}, 0},
		{"missing directory", []string{"validate", "-dir", t.TempDir()}, 1},
		{"unknown command", []string{"query"}, 2},
		{"extra argument", []string{"validate", "extra"}, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if got := run(tt.args, &out, &errOut); got != tt.code {
				t.Fatalf("exit %d: %s", got, errOut.String())
			}
			if tt.code == 0 && !strings.Contains(out.String(), "sha256:") {
				t.Fatal("missing hash")
			}
			if tt.code != 0 && (out.Len() != 0 || errOut.Len() == 0) {
				t.Fatal("invalid failure output")
			}
		})
	}
}
