package app

import (
	"context"
	"errors"
	metadata "github.com/nightnoryu/tesseract/internal/metadata/domain"
	semantic "github.com/nightnoryu/tesseract/internal/semantic/domain"
	catalog "github.com/nightnoryu/tesseract/internal/semantic/infrastructure"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
	"os"
	"testing"
	"time"
)

type memoryStore struct{ tables []metadata.Table }

func (s *memoryStore) Scan(ctx context.Context, source, hash string, inspect func() []metadata.Table) error {
	s.tables = inspect()
	return ctx.Err()
}

type inspector struct{ calls int }

func (i *inspector) InspectCatalogTables(ctx context.Context, ids []string) ([]source.Table, error) {
	i.calls++
	<-ctx.Done()
	return nil, ctx.Err()
}
func (*inspector) ExecuteQuery(context.Context, source.ValidatedQuery) (source.Result, error) {
	panic("business query in ingestion")
}
func (*inspector) Close() error { return nil }
func TestFailurePublishesUnavailable(t *testing.T) {
	c, err := catalog.Load(os.DirFS("../../../../catalog"))
	if err != nil {
		t.Fatal(err)
	}
	for _, timeout := range []bool{false, true} {
		s := &memoryStore{}
		conn := &inspector{}
		w := Worker{Catalog: c, Store: s, Timeout: 10 * time.Millisecond, Open: func(context.Context, semantic.Catalog, string) (source.Connector, error) {
			if timeout {
				return conn, nil
			}
			return nil, errors.New("secret must not persist")
		}}
		if err := w.Scan(context.Background(), c.Sources[0].ID); err != nil {
			t.Fatal(err)
		}
		if len(s.tables) != 1 || s.tables[0].Reason != "scan_failed" {
			t.Fatalf("%+v", s.tables)
		}
		if err := w.Scan(context.Background(), "unlisted"); err == nil {
			t.Fatal("unlisted source accepted")
		}
	}
}

type partialInspector struct{ calls []string }

func (i *partialInspector) InspectCatalogTables(_ context.Context, ids []string) ([]source.Table, error) {
	i.calls = append(i.calls, ids...)
	if ids[0] == "leads" {
		return nil, errors.New("connection failed")
	}
	return []source.Table{{Dataset: ids[0], Schema: "mart", Name: "Other", Present: false}}, nil
}
func (*partialInspector) ExecuteQuery(context.Context, source.ValidatedQuery) (source.Result, error) {
	panic("business query in ingestion")
}
func (*partialInspector) Close() error { return nil }
func TestPartialFailureKeepsCompleteCatalogSnapshot(t *testing.T) {
	c, err := catalog.Load(os.DirFS("../../../../catalog"))
	if err != nil {
		t.Fatal(err)
	}
	other := c.Datasets[0]
	other.ID = "other"
	other.Schema = "mart"
	other.Table = "Other"
	c.Datasets = append(c.Datasets, other)
	s := &memoryStore{}
	conn := &partialInspector{}
	w := Worker{Catalog: c, Store: s, Timeout: time.Second, Open: func(context.Context, semantic.Catalog, string) (source.Connector, error) { return conn, nil }}
	if err := w.Scan(context.Background(), c.Sources[0].ID); err != nil {
		t.Fatal(err)
	}
	if len(conn.calls) != 2 || len(s.tables) != 2 || s.tables[0].Reason != "scan_failed" || s.tables[1].Reason != "missing_table" {
		t.Fatalf("calls=%v tables=%+v", conn.calls, s.tables)
	}
}
