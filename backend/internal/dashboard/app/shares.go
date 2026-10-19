package app

import (
	"context"
	"github.com/nightnoryu/tesseract/internal/dashboard/domain"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
	"time"
)

func (s Service) QueryWidget(ctx context.Context, user, id, widget string, expected int64, filters []domain.Filter) (source.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	d, err := s.Store.Read(ctx, user, id)
	if err != nil {
		return source.Result{}, err
	}
	if d.Revision != expected {
		return source.Result{}, domain.ErrConflict
	}
	// Only values of existing filters can change; mappings and queries stay server-owned.
	if len(filters) != len(d.Spec.Filters) {
		return source.Result{}, domain.ErrInvalid
	}
	for i, f := range filters {
		if f.ID != d.Spec.Filters[i].ID || f.Operator != d.Spec.Filters[i].Operator {
			return source.Result{}, domain.ErrInvalid
		}
	}
	d.Spec.Filters = filters
	if err = d.Spec.Validate(); err != nil {
		return source.Result{}, err
	}
	for _, w := range d.Spec.Widgets {
		if w.ID == widget {
			return s.Policy.Execute(ctx, s.Engine, user, d.Spec.Query(w))
		}
	}
	return source.Result{}, domain.ErrNotFound
}
