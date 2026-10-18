package domain

import (
	"context"
	"errors"
)

var (
	ErrNotCataloged = errors.New("dwh_object_not_cataloged")
	ErrInvalidQuery = errors.New("dwh_query_not_validated")
	ErrAccessDenied = errors.New("dwh_access_denied")
	ErrUnavailable  = errors.New("dwh_unavailable")
	ErrReadOnly     = errors.New("dwh_read_only_credentials_required")
	ErrLimit        = errors.New("dwh_result_limit_exceeded")
)

type Column struct {
	Name      string
	Type      string
	Nullable  bool
	MaxLength int
	Precision int
	Scale     int
}
type Table struct {
	Dataset string
	Schema  string
	Name    string
	Present bool
	Columns []Column
}

// ValidatedQuery is opaque. Milestone 8 will mint these only after catalog,
// schema compatibility and caller entitlement validation. There is deliberately
// no SQL field or public constructor.
type ValidatedQuery struct {
	dataset     string
	measure     string
	catalogHash string
}

func (q ValidatedQuery) Dataset() string     { return q.dataset }
func (q ValidatedQuery) Measure() string     { return q.measure }
func (q ValidatedQuery) CatalogHash() string { return q.catalogHash }

type Result struct {
	Columns []string
	Rows    [][]any
}
type Connector interface {
	InspectCatalogTables(context.Context, []string) ([]Table, error)
	ExecuteQuery(context.Context, ValidatedQuery) (Result, error)
	Close() error
}
