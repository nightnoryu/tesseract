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

type Result struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}
type Connector interface {
	InspectCatalogTables(context.Context, []string) ([]Table, error)
	ExecuteQuery(context.Context, ValidatedQuery) (Result, error)
	Close() error
}
