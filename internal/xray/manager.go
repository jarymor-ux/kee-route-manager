package xray

import (
	"context"
	"sync"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/amnezia"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"errors"
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
	awg *amnezia.Runtime
}

func NewManager(c config.Config, r Runner, p Platform) *Manager { return &Manager{cfg: c, r: r, p: p} }
func (m *Manager) UseAWGRuntime(rt *amnezia.Runtime){ m.awg=rt }
func (m *Manager) ensureAWGPool(ctx context.Context, slots []model.Slot, nodes map[string]model.Node)error {
	for _,slot:=range slots {
		node,ok:=nodes[slot.NodeID]
		if !ok||node.Protocol!="amneziawg" {continue}
		if m.awg==nil {return errors.New("AmneziaWG runtime unavailable")}
		if _,_,err:=m.awg.Ensure(ctx,node.AWGProfileID);err!=nil {return err}
	}
	return nil
}
