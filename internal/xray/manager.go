package xray

import (
	"context"
	"sync"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

type Runner interface {
	Run(context.Context, []string) ([]byte, error)
}
type Platform interface {
	RestartXray(context.Context) error
	XrayRunning(context.Context) bool
}
type Manager struct {
	cfg config.Config
	r   Runner
	p   Platform
	mu  sync.Mutex
}

func NewManager(c config.Config, r Runner, p Platform) *Manager { return &Manager{cfg: c, r: r, p: p} }
