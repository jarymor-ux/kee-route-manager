package core

import (
	"context"
	"errors"

	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/operation"
	"github.com/jarymor-ux/kee-route-manager/internal/subscription"
)

type subscriptionUpdateFetcher interface {
	SetAutoUpdatePaused(bool)
	FetchCached(context.Context, map[string]model.SourceState) subscription.Result
	RefreshAll(context.Context, map[string]model.SourceState) subscription.Result
}

type SubscriptionUpdateStatus struct {
	AutoUpdateEnabled bool                         `json:"auto_update_enabled"`
	CacheEnabled      bool                         `json:"cache_enabled"`
	States            map[string]model.SourceState `json:"states"`
}

func (m *Manager) SubscriptionUpdateStatus(ctx context.Context) SubscriptionUpdateStatus {
	state := m.store.State()
	status := SubscriptionUpdateStatus{AutoUpdateEnabled: !state.SubscriptionUpdatesPaused, CacheEnabled: m.cfg.Subscriptions.CacheEnabled, States: map[string]model.SourceState{}}
	if f, ok := m.fetcher.(subscriptionUpdateFetcher); ok {
		// Read actual copies: a successful no-cache download is not a saved copy.
		status.States = f.FetchCached(ctx, state.Sources).States
	}
	return status
}

func (m *Manager) SetSubscriptionAutoUpdate(ctx context.Context, enabled bool) error {
	f, ok := m.fetcher.(subscriptionUpdateFetcher)
	if !ok {
		return errors.New("subscription update management unavailable")
	}
	return m.RunAction(ctx, "subscription-auto-update", "web", func(ctx context.Context) error {
		// Routing commits must retain the preference, including restore. Pending
		// replay could replace it, so refuse until reconciliation finishes.
		m.routeMu.Lock()
		defer m.routeMu.Unlock()
		m.subscriptionMu.Lock()
		defer m.subscriptionMu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		if pending, err := m.store.PendingTransaction(); err != nil {
			return err
		} else if pending != nil {
			return operation.ErrBusy
		}
		if err := m.store.Update(func(s *model.State) error {
			s.SubscriptionUpdatesPaused = !enabled
			return nil
		}); err != nil {
			return err
		}
		// Persist first; changing the policy cancels automatic I/O and prevents
		// obsolete downloads from replacing the last saved copies.
		f.SetAutoUpdatePaused(!enabled)
		m.sourcesVersion++
		return nil
	})
}

func (m *Manager) RefreshSubscriptions(ctx context.Context) error {
	f, ok := m.fetcher.(subscriptionUpdateFetcher)
	if !ok {
		return errors.New("subscription update management unavailable")
	}
	err := m.RunAction(ctx, "subscription-refresh", "web", func(ctx context.Context) error {
		if !m.sourceRefreshMu.TryLock() {
			return operation.ErrBusy
		}
		defer m.sourceRefreshMu.Unlock()
		m.subscriptionMu.Lock()
		m.sourcesVersion++
		revision := m.sourcesVersion
		m.subscriptionMu.Unlock()
		result := f.RefreshAll(ctx, m.store.State().Sources)
		m.subscriptionMu.Lock()
		defer m.subscriptionMu.Unlock()
		if revision != m.sourcesVersion {
			return operation.ErrSuperseded
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.store.Update(func(s *model.State) error {
			s.Sources = result.States
			return nil
		}); err != nil {
			return err
		}
		return errors.Join(result.Errors...)
	})
	if err == nil {
		// Test the just-saved copies without making a second provider request.
		// This also honors the separate automatic-routing pause after restore.
		m.queueConfigurationBenchmark("subscription-refresh")
	}
	return err
}
