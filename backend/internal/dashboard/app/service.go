package app

import (
	"context"
	"errors"
	access "github.com/nightnoryu/tesseract/internal/access/app"
	analytics "github.com/nightnoryu/tesseract/internal/analytics/app"
	"github.com/nightnoryu/tesseract/internal/dashboard/domain"
	source "github.com/nightnoryu/tesseract/internal/source/domain"
	"time"
)

type Store interface {
	Create(context.Context, string, domain.Spec) (domain.Dashboard, error)
	Get(context.Context, string, string) (domain.Dashboard, error)
	List(context.Context, string, int, int) ([]domain.Dashboard, error)
	Save(context.Context, string, string, int64, domain.Spec, bool) (domain.Dashboard, error)
	Delete(context.Context, string, string, int64) error
	History(context.Context, string, string, int, int) ([]domain.Revision, error)
	CreateTurn(context.Context, string, domain.Spec, domain.Turn) (domain.Dashboard, error)
	SaveTurn(context.Context, string, string, int64, domain.Spec, domain.Turn) (domain.Dashboard, error)
	RecordTurn(context.Context, string, string, int64, domain.Turn) error
	Turns(context.Context, string, string, int, int) ([]domain.Turn, error)
	Revision(context.Context, string, string, int64) (domain.Spec, error)
}
type Service struct {
	Store  Store
	Engine analytics.Engine
	Policy *access.Policy
}

func (s Service) states(ctx context.Context, user string, spec domain.Spec) ([]domain.State, error) {
	states := make([]domain.State, 0, len(spec.Widgets))
	hash, err := s.Engine.Catalog.Hash()
	if err != nil {
		return nil, err
	}
	for _, w := range spec.Widgets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		status := "ready"
		g, err := s.Policy.Resolve(user, w.Query.Dataset, hash)
		if err == nil {
			_, err = s.Engine.Validate(ctx, spec.Query(w), g)
		}
		switch {
		case err == nil:
		case errors.Is(err, source.ErrAccessDenied):
			status = "denied"
		case errors.Is(err, source.ErrSchemaUnavailable):
			status = "unavailable"
		case errors.Is(err, source.ErrInvalidQuery), errors.Is(err, source.ErrLimit):
			status = "invalid"
		default:
			return nil, err
		}
		states = append(states, domain.State{ID: w.ID, Status: status})
	}
	return states, nil
}
func (s Service) validate(ctx context.Context, user string, spec domain.Spec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	states, err := s.states(ctx, user, spec)
	if err != nil {
		return err
	}
	for _, state := range states {
		switch state.Status {
		case "denied":
			return source.ErrAccessDenied
		case "unavailable":
			return source.ErrSchemaUnavailable
		case "invalid":
			return domain.ErrInvalid
		}
	}
	return nil
}
func (s Service) decorate(ctx context.Context, user string, d domain.Dashboard, err error) (domain.Dashboard, error) {
	if err != nil {
		return d, err
	}
	d.WidgetStates, err = s.states(ctx, user, d.Spec)
	return d, err
}
func (s Service) Create(ctx context.Context, user string, spec domain.Spec) (domain.Dashboard, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.validate(ctx, user, spec); err != nil {
		return domain.Dashboard{}, err
	}
	d, err := s.Store.Create(ctx, user, spec)
	return s.decorate(ctx, user, d, err)
}
func (s Service) Get(ctx context.Context, user, id string) (domain.Dashboard, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d, err := s.Store.Get(ctx, user, id)
	return s.decorate(ctx, user, d, err)
}
func (s Service) Save(ctx context.Context, user, id string, expected int64, spec domain.Spec, publish bool) (domain.Dashboard, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d, err := s.Store.Get(ctx, user, id)
	if err != nil {
		return d, err
	}
	if expected < 1 || d.Revision != expected {
		return d, domain.ErrConflict
	}
	if publish {
		spec = d.Spec
	}
	if err := s.validate(ctx, user, spec); err != nil {
		return domain.Dashboard{}, err
	}
	d, err = s.Store.Save(ctx, user, id, expected, spec, publish)
	return s.decorate(ctx, user, d, err)
}
func (s Service) List(ctx context.Context, user string, limit, offset int) ([]domain.Dashboard, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ds, err := s.Store.List(ctx, user, limit, offset)
	if err != nil {
		return nil, err
	}
	for i := range ds {
		ds[i], err = s.decorate(ctx, user, ds[i], nil)
		if err != nil {
			return nil, err
		}
	}
	return ds, nil
}
func (s Service) History(ctx context.Context, user, id string, limit, offset int) ([]domain.Revision, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rs, err := s.Store.History(ctx, user, id, limit, offset)
	if err != nil {
		return nil, err
	}
	for i := range rs {
		rs[i].WidgetStates, err = s.states(ctx, user, rs[i].Spec)
		if err != nil {
			return nil, err
		}
	}
	return rs, nil
}

func (s Service) Delete(ctx context.Context, user, id string, expected int64) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.Store.Delete(ctx, user, id, expected)
}
