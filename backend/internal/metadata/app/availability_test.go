package app

import (
	"context"
	"github.com/nightnoryu/tesseract/internal/metadata/domain"
	catalog "github.com/nightnoryu/tesseract/internal/semantic/infrastructure"
	"os"
	"testing"
	"time"
)

type snapshotReader struct {
	snapshot domain.Snapshot
	hash     string
}

func (s *snapshotReader) Latest(_ context.Context, _, hash string) (domain.Snapshot, error) {
	s.hash = hash
	return s.snapshot, nil
}
func TestAvailabilityBoundary(t *testing.T) {
	cat, err := catalog.Load(os.DirFS("../../../../catalog"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, tc := range []struct {
		name, reason    string
		age             time.Duration
		present, absent bool
		want            string
		stale           bool
	}{
		{name: "compatible", present: true},
		{name: "never scanned", absent: true, want: "not_scanned"},
		{name: "expired", present: true, age: time.Hour, want: "stale", stale: true},
		{name: "future timestamp", present: true, age: -time.Minute, want: "stale", stale: true},
		{name: "drift", present: true, reason: "incompatible_type", want: "incompatible_type"},
		{name: "expired drift", present: true, reason: "missing_column", age: time.Hour, want: "missing_column", stale: true},
		{name: "missing", want: "missing_table"},
		{name: "denied", reason: "access_denied", want: "access_denied"},
		{name: "unknown reason", reason: "sensitive driver text", want: "scan_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := cat.Datasets[0]
			reader := &snapshotReader{snapshot: domain.Snapshot{LastScan: now}}
			if !tc.absent {
				reader.snapshot.Tables = []domain.Table{{Dataset: d.ID, Schema: d.Schema, Name: d.Table, Present: tc.present, Reason: tc.reason, InspectedAt: now.Add(-tc.age)}}
			}
			reader.snapshot.Tables = append(reader.snapshot.Tables, domain.Table{Dataset: "unlisted", Schema: "secret", Name: "Private", Present: true})
			a := Availability{Catalog: cat, Store: reader, MaxAge: 15 * time.Minute}
			hash, sources, err := a.Read(context.Background(), now)
			if err != nil {
				t.Fatal(err)
			}
			expectedHash, _ := cat.Hash()
			if hash != expectedHash || reader.hash != hash {
				t.Fatal("wrong catalog version")
			}
			s := sources[0]
			if s.Configured != 1 || len(s.Tables) != 1 || s.Configured != s.Available+s.Unavailable {
				t.Fatalf("counts: %+v", s)
			}
			table := s.Tables[0]
			if table.Reason != tc.want || table.Stale != tc.stale {
				t.Fatalf("table: %+v", table)
			}
			if (s.Available == 1) != (tc.want == "") {
				t.Fatal("availability count")
			}
		})
	}
}
