package infrastructure

import (
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func fixture(t *testing.T) fstest.MapFS {
	t.Helper()
	f := fstest.MapFS{}
	for _, p := range []string{"sources.toml", "datasets/leads.toml"} {
		b, err := os.ReadFile("../../../../catalog/" + p)
		if err != nil {
			t.Fatal(err)
		}
		f[p] = &fstest.MapFile{Data: b}
	}
	return f
}
func TestRejectInvalidCatalog(t *testing.T) {
	cases := []struct{ name, path, old, new string }{
		{"unknown key", "sources.toml", "version = 1", "version = 1\npassword = 'secret'"},
		{"version", "sources.toml", "version = 1", "version = 2"},
		{"connector", "sources.toml", "mssql", "postgres"},
		{"secret literal", "sources.toml", "TESSERACT_DWH_CRM_DSN", "sqlserver://password"},
		{"source reference", "datasets/leads.toml", "source = \"crm_dwh\"", "source = \"invented\""},
		{"column reference", "datasets/leads.toml", "column = \"lead_id\"", "column = \"invented\""},
		{"duplicate column", "datasets/leads.toml", "id = \"channel\"\nsource_column", "id = \"lead_id\"\nsource_column"},
		{"duplicate physical column", "datasets/leads.toml", "ChannelName", "leadid"},
		{"formula", "datasets/leads.toml", "operation = \"count_distinct\"", "operation = \"count_distinct\"\nformula = \"COUNT(*)\""},
		{"operation", "datasets/leads.toml", "count_distinct", "custom_sql"},
		{"sum string", "datasets/leads.toml", "count_distinct", "sum"},
		{"table injection", "datasets/leads.toml", "MarketingLeads", "MarketingLeads; DROP TABLE x"},
		{"column expression", "datasets/leads.toml", "ChannelName", "LOWER(ChannelName)"},
		{"hidden dimension", "datasets/leads.toml", "column = \"channel\"", "column = \"lead_id\""},
		{"missing exposure", "datasets/leads.toml", "expose_to_analytics = false", ""},
		{"type", "datasets/leads.toml", "type = \"string\"", "type = \"sql\""},
		{"grain", "datasets/leads.toml", "\"day\", \"month\"", "\"hour\""},
		{"operator", "datasets/leads.toml", "\"eq\", \"ne\"", "\"like_sql\", \"ne\""},
		{"string ordered filter", "datasets/leads.toml", "\"eq\", \"ne\"", "\"gt\", \"ne\""},
		{"synonyms", "datasets/leads.toml", "[\"lead acquisition\"]", "[\"leads\", \"LEADS\"]"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := fixture(t)
			original := string(f[tt.path].Data)
			if !strings.Contains(original, tt.old) {
				t.Fatal("test mutation missing")
			}
			f[tt.path].Data = []byte(strings.ReplaceAll(original, tt.old, tt.new))
			c, err := Load(f)
			if err == nil {
				t.Fatal("accepted invalid catalog")
			}
			if len(c.Sources) != 0 {
				t.Fatal("returned partial catalog")
			}
		})
	}
	t.Run("duplicate dataset", func(t *testing.T) {
		f := fixture(t)
		f["datasets/copy.toml"] = f["datasets/leads.toml"]
		if _, err := Load(f); err == nil {
			t.Fatal("accepted duplicate")
		}
	})
	t.Run("empty datasets", func(t *testing.T) {
		f := fixture(t)
		delete(f, "datasets/leads.toml")
		if _, err := Load(f); err == nil {
			t.Fatal("accepted empty catalog")
		}
	})
}
func TestStableHash(t *testing.T) {
	f := fixture(t)
	c, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	want, err := c.Hash()
	if err != nil {
		t.Fatal(err)
	}
	f["datasets/renamed.toml"] = f["datasets/leads.toml"]
	delete(f, "datasets/leads.toml")
	f["sources.toml"].Data = append([]byte("# formatting changes do not change the catalog\n"), f["sources.toml"].Data...)
	other, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(other.Datasets[0].Columns)
	slices.Reverse(other.Datasets[0].Dimensions)
	for i := range other.Datasets[0].Dimensions {
		slices.Reverse(other.Datasets[0].Dimensions[i].Operators)
	}
	got, err := other.Hash()
	if err != nil || got != want {
		t.Fatalf("unstable hash: %s %s %v", want, got, err)
	}
	for i := range other.Datasets[0].Dimensions {
		other.Datasets[0].Dimensions[i].Synonyms = []string{}
	}
	got, err = other.Hash()
	if err != nil || got != want {
		t.Fatal("empty optional lists changed hash")
	}
	original := other.Datasets[0].Columns[0].ID
	_, _ = other.Hash()
	if other.Datasets[0].Columns[0].ID != original {
		t.Fatal("hash mutated catalog")
	}
	other.Datasets[0].Description = "Changed definition"
	got, err = other.Hash()
	if err != nil || got == want {
		t.Fatal("hash did not reflect definition change")
	}
}
