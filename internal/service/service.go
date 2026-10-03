// Package service owns transactions and publication; protocol and SSH behavior
// remain in the upstream core. Management APIs can reuse this boundary.
package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/wentf9/xops-cli/core/mcp/state"
	"github.com/wentf9/xops-mcp/internal/adapters/xops"
	"github.com/wentf9/xops-mcp/internal/storage"
)

var ErrPending = errors.New("inventory persistence or publication is pending; reconcile without repeating the write")

type Service struct {
	store       storage.Repository
	Coordinator *state.Coordinator
	retire      state.Retire
	writes      chan struct{}
	done        chan struct{}
	closeOnce   sync.Once
	pending     *pending
}
type pending struct {
	update        *state.Update
	before, after storage.Inventory
	confirmed     bool
}

func New(ctx context.Context, store storage.Repository, retire state.Retire) (*Service, error) {
	v, err := store.Load(ctx)
	if err != nil {
		return nil, err
	}
	view, _, err := xops.Snapshot(v)
	if err != nil {
		return nil, err
	}
	c, err := state.New(view, state.Options{})
	if err != nil {
		return nil, err
	}
	return &Service{store: store, Coordinator: c, retire: retire, writes: make(chan struct{}, 1), done: make(chan struct{})}, nil
}

func (s *Service) lock(ctx context.Context) error {
	select {
	case <-s.done:
		return state.ErrClosed
	default:
	}
	select {
	case s.writes <- struct{}{}:
		return nil
	case <-s.done:
		return state.ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) Apply(ctx context.Context, expected uint64, candidate storage.Inventory) (uint64, error) {
	candidate = candidate.Clone()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.lock(ctx); err != nil {
		return 0, err
	}
	defer func() { <-s.writes }()
	if s.pending != nil {
		return 0, ErrPending
	}
	before, err := s.store.Load(ctx)
	if err != nil {
		return 0, err
	}
	if before.Revision != expected || candidate.DomainID != before.DomainID {
		return before.Revision, storage.ErrConflict
	}
	candidate.Revision = expected + 1
	view, sources, err := xops.Snapshot(candidate)
	if err != nil {
		return before.Revision, err
	}
	update, err := s.Coordinator.BeginUpdate(ctx, fmt.Sprint(expected), view)
	if err != nil {
		return before.Revision, err
	}
	if err := update.BeginPersistence(); err != nil {
		return before.Revision, errors.Join(err, update.Abort())
	}
	s.pending = &pending{update: update, before: before, after: candidate}
	if err := s.store.Save(ctx, expected, candidate, sources); err != nil {
		// A commit error may occur after durable application. Re-read with a
		// separate bounded context, preserving the admission barrier on doubt.
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		reconcileErr := s.reconcile(cleanup)
		return candidate.Revision, errors.Join(fmt.Errorf("persist inventory; inspect revision before retry: %w", err), reconcileErr)
	}
	if err := update.ConfirmCommit(); err != nil {
		return candidate.Revision, errors.Join(ErrPending, err)
	}
	s.pending.confirmed = true
	if err := update.Publish(ctx, s.retire); err != nil {
		return candidate.Revision, errors.Join(ErrPending, err)
	}
	s.pending = nil
	return candidate.Revision, nil
}

func (s *Service) Reconcile(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer func() { <-s.writes }()
	return s.reconcile(ctx)
}

func (s *Service) reconcile(ctx context.Context) error {
	p := s.pending
	if p == nil {
		return nil
	}
	actual, err := s.store.Load(ctx)
	if err != nil {
		return errors.Join(ErrPending, err)
	}
	if !p.confirmed && actual.Revision == p.before.Revision {
		if err := p.update.ConfirmRollback(); err != nil {
			return errors.Join(ErrPending, err)
		}
		s.pending = nil
		return nil
	}
	// Compare executable views as well as revision, so uncertain persistence
	// cannot activate a candidate different from the authoritative database.
	actualView, _, err := xops.Snapshot(actual)
	if err != nil {
		return errors.Join(ErrPending, err)
	}
	nextView, _, err := xops.Snapshot(p.after)
	if err != nil {
		return errors.Join(ErrPending, err)
	}
	if !reflect.DeepEqual(actualView, nextView) {
		return ErrPending
	}
	if !p.confirmed {
		if err := p.update.ConfirmCommit(); err != nil {
			return errors.Join(ErrPending, err)
		}
		p.confirmed = true
	}
	if err := p.update.Publish(ctx, s.retire); err != nil {
		return errors.Join(ErrPending, err)
	}
	s.pending = nil
	return nil
}

// Close is called after the runtime has drained committed operations. The
// writer channel joins every bounded transaction before closing the gate.
func (s *Service) Close() error {
	s.closeOnce.Do(func() { close(s.done) })
	s.writes <- struct{}{}
	defer func() { <-s.writes }()
	return s.Coordinator.Close()
}
