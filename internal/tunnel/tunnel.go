// Package tunnel defines the controller's tunnel backend contract.
package tunnel

import (
	"context"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"net/url"
)

type CoreCapabilities struct {
	DynamicPool         bool `json:"dynamic_pool"`
	PersistentSelection bool `json:"persistent_selection"`
}
type Selection struct {
	Tag string `json:"tag"`
}
type DesiredPool struct {
	Slots      []model.Slot          `json:"slots"`
	Previous   []model.Slot          `json:"previous,omitempty"`
	Nodes      map[string]model.Node `json:"nodes"`
	ActiveSlot int                   `json:"active_slot"`
	Selection  Selection             `json:"selection"`
}
type ActualCoreState struct {
	Drift      string    `json:"drift,omitempty"`
	Running    bool      `json:"running"`
	Configured bool      `json:"configured"`
	Selection  Selection `json:"selection"`
	ConfigHash string    `json:"config_hash,omitempty"`
}
type TunnelCore interface {
	Name() string
	Capabilities() CoreCapabilities
	Bootstrap(context.Context, DesiredPool) error
	ApplyPool(context.Context, DesiredPool) error
	Select(context.Context, Selection) error
	EnterDirect(context.Context) error
	ProbeEndpoint(int) (*url.URL, error)
	HealthEndpoint() (*url.URL, error)
	Ready(context.Context) error
	ActualState(context.Context) (ActualCoreState, error)
	Restore(context.Context) error
}
