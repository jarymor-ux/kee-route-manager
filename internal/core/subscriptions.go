package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
)

type managedSourceFetcher interface {
	Sources() []config.Source
	ReplaceSources([]config.Source) error
}

func (m *Manager) SubscriptionSources() []config.Source {
	fetcher, ok := m.fetcher.(managedSourceFetcher)
	if !ok {
		return nil
	}
	return fetcher.Sources()
}

func (m *Manager) SaveSubscription(_ context.Context, source config.Source) error {
	m.subscriptionMu.Lock()
	defer m.subscriptionMu.Unlock()

	fetcher, ok := m.fetcher.(managedSourceFetcher)
	if !ok {
		return errors.New("subscription source management unavailable")
	}
	source.ID = strings.TrimSpace(source.ID)
	source.Name = strings.TrimSpace(source.Name)
	source.URL = strings.TrimSpace(source.URL)

	next := fetcher.Sources()
	replaced := false
	for i := range next {
		if next[i].ID == source.ID {
			next[i] = cloneSubscriptionSource(source)
			replaced = true
			break
		}
	}
	if !replaced {
		next = append(next, cloneSubscriptionSource(source))
	}
	if err := m.validateSubscriptionSources(next); err != nil {
		return err
	}
	if err := fetcher.ReplaceSources(next); err != nil {
		return fmt.Errorf("persist subscription sources: %w", err)
	}

	if m.store != nil {
		_ = m.store.Update(func(state *model.State) error {
			current, exists := state.Sources[source.ID]
			if !source.Enabled {
				delete(state.Sources, source.ID)
			} else if exists {
				current.Name = source.Name
				state.Sources[source.ID] = current
			}
			return nil
		})
	}
	m.queueSubscriptionBenchmark()
	return nil
}

func (m *Manager) DeleteSubscription(_ context.Context, id string) error {
	m.subscriptionMu.Lock()
	defer m.subscriptionMu.Unlock()

	fetcher, ok := m.fetcher.(managedSourceFetcher)
	if !ok {
		return errors.New("subscription source management unavailable")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("subscription id is required")
	}
	current := fetcher.Sources()
	next := make([]config.Source, 0, len(current))
	found := false
	for _, source := range current {
		if source.ID == id {
			found = true
			continue
		}
		next = append(next, cloneSubscriptionSource(source))
	}
	if !found {
		return errors.New("subscription not found")
	}
	if err := m.validateSubscriptionSources(next); err != nil {
		return err
	}
	if err := fetcher.ReplaceSources(next); err != nil {
		return fmt.Errorf("persist subscription sources: %w", err)
	}
	if m.store != nil {
		_ = m.store.Update(func(state *model.State) error {
			delete(state.Sources, id)
			return nil
		})
	}
	m.queueSubscriptionBenchmark()
	return nil
}

func (m *Manager) validateSubscriptionSources(sources []config.Source) error {
	candidate := m.cfg
	candidate.Subscriptions.Sources = cloneSubscriptionSources(sources)
	if err := candidate.Validate(); err != nil {
		return fmt.Errorf("invalid subscription configuration: %w", err)
	}
	return nil
}

func (m *Manager) queueSubscriptionBenchmark() {
	m.lifeMu.Lock()
	started := m.ctx != nil && !m.stopping
	m.lifeMu.Unlock()
	if started {
		m.queueBenchmark("subscription-change")
	}
}

func cloneSubscriptionSources(in []config.Source) []config.Source {
	out := make([]config.Source, len(in))
	for i := range in {
		out[i] = cloneSubscriptionSource(in[i])
	}
	return out
}

func cloneSubscriptionSource(in config.Source) config.Source {
	out := in
	if in.Headers != nil {
		out.Headers = make(map[string]string, len(in.Headers))
		for key, value := range in.Headers {
			out.Headers[key] = value
		}
	}
	return out
}
