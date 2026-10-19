package app

import (
	"context"
	"github.com/nightnoryu/tesseract/internal/dashboard/domain"
	"time"
)

func (s Service) CreateTurn(ctx context.Context, user string, spec domain.Spec, turn domain.Turn) (domain.Dashboard, error) {
	if err := s.validate(ctx, user, spec); err != nil {
		return domain.Dashboard{}, err
	}
	d, err := s.Store.CreateTurn(ctx, user, spec, turn)
	return s.decorateCommitted(ctx, user, d, err)
}
func (s Service) Edit(ctx context.Context, user, id string, expected int64, ops []domain.Operation, entries []domain.Entry) (domain.Dashboard, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d, err := s.Store.Get(ctx, user, id)
	if err != nil {
		return domain.Dashboard{}, err
	}
	if expected < 1 || d.Revision != expected {
		return domain.Dashboard{}, domain.ErrConflict
	}
	spec, err := domain.Apply(d.Spec, ops)
	if err != nil {
		return domain.Dashboard{}, err
	}
	if err = s.validate(ctx, user, spec); err != nil {
		return domain.Dashboard{}, err
	}
	d, err = s.Store.SaveTurn(ctx, user, id, expected, spec, domain.Turn{Kind: "edit", Entries: entries, Operations: ops})
	return s.decorateCommitted(ctx, user, d, err)
}
func (s Service) Discard(ctx context.Context, user, id string, expected int64) (domain.Dashboard, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d, err := s.Store.Get(ctx, user, id)
	if err != nil {
		return domain.Dashboard{}, err
	}
	if expected < 1 || d.Revision != expected {
		return domain.Dashboard{}, domain.ErrConflict
	}
	target := d.PublishedRevision
	if target == 0 {
		target = 1
	}
	spec, err := s.Store.Revision(ctx, user, id, target)
	if err != nil {
		return domain.Dashboard{}, err
	}
	// Restoring an already stored revision remains possible after schema drift.
	// Rendering rechecks availability; publishing still requires valid queries.
	d, err = s.Store.SaveTurn(ctx, user, id, expected, spec, domain.Turn{Kind: "discard", Entries: []domain.Entry{{Role: "assistant", Content: "Discarded changes and restored the last published revision, or the initial draft if unpublished."}}})
	return s.decorateCommitted(ctx, user, d, err)
}
func (s Service) Turns(ctx context.Context, user, id string, limit, offset int) ([]domain.Turn, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.Store.Turns(ctx, user, id, limit, offset)
}

// Once committed, a decoration failure must not invite another mutation.
func (s Service) decorateCommitted(ctx context.Context, user string, d domain.Dashboard, err error) (domain.Dashboard, error) {
	if err != nil {
		return domain.Dashboard{}, err
	}
	result, err := s.decorate(ctx, user, d, nil)
	if err == nil {
		return result, nil
	}
	d.WidgetStates = make([]domain.State, 0, len(d.Spec.Widgets))
	for _, w := range d.Spec.Widgets {
		d.WidgetStates = append(d.WidgetStates, domain.State{ID: w.ID, Status: "unavailable"})
	}
	return d, nil
}
