package app

import (
	"context"
	"errors"
	semantic "github.com/nightnoryu/tesseract/internal/semantic/domain"
	"github.com/nightnoryu/tesseract/internal/source/domain"
	"github.com/nightnoryu/tesseract/internal/source/infrastructure/mssql"
)

func Open(ctx context.Context, c semantic.Catalog, id string, lookup func(string) string, cfg mssql.Config) (domain.Connector, error) {
	for _, s := range c.Sources {
		if s.ID != id {
			continue
		}
		if s.Kind != "mssql" {
			return nil, errors.New("unsupported_dwh_connector_kind")
		}
		return mssql.Open(ctx, c, id, lookup(s.SecretEnv), cfg)
	}
	return nil, errors.New("unknown_dwh_source")
}
