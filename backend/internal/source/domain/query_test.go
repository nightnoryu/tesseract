package domain

import (
	"strings"
	"testing"
)

func TestStrictQueryInput(t *testing.T) {
	for _, input := range []string{
		`{"dataset":"leads","sql":"SELECT 1"}`,
		`{"filters":[{"dimension":"channel","operator":"eq","values":[{"type":"string"}]}]}`,
		`{"filters":[{"dimension":"channel","operator":"eq","values":[{"type":"string","value":null}]}]}`,
		`{"filters":[{"dimension":"channel","expression":"1=1"}]}`,
		`{"dataset":"leads"} {"dataset":"other"}`,
		strings.Repeat(" ", MaxQueryBytes+1),
	} {
		if _, err := DecodeQuery(strings.NewReader(input)); err == nil {
			t.Fatal("accepted unknown/oversized request")
		}
	}
	if _, err := DecodeQuery(strings.NewReader(`{"filters":[{"dimension":"channel","operator":"eq","values":[{"type":"string","value":""}]}]}`)); err != nil {
		t.Fatal("explicit empty string rejected")
	}

	for _, v := range []Value{{"integer", "9223372036854775808"}, {"decimal", "1e10"}, {"decimal", "0.0000000000000000001"}, {"decimal", "1; SELECT 1"}, {"boolean", "1"}, {"date", "2026-02-30"}, {"datetime", "2026-01-01"}, {"datetime", "0000-01-01T00:00:00Z"}, {"string", strings.Repeat("a", 2049)}} {
		if _, err := v.Parameter(); err == nil {
			t.Fatalf("accepted invalid scalar %+v", v)
		}
	}
}
