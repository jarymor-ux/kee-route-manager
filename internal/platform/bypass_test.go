package platform

import (
	"context"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"testing"
)

func TestKeeneticAdvertisesNoUntestedBypass(t *testing.T) {
	p := newKeenetic(config.Default(), Runner{})
	if p.Capabilities().DirectBypass {
		t.Fatal("untested Keenetic bypass advertised")
	}
	if err := p.EnterDirectBypass(context.Background()); err == nil {
		t.Fatal("unsupported bypass reported success")
	}
}
