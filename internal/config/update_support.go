package config

import "fmt"

// UpdateApplySupport is deliberately separate from Validate: managed routing
// remains supported, but its firewall/policy resources cannot yet be reconciled
// read-only by an update candidate before the durable activation decision.
func (c Config) UpdateApplySupport() error {
	kind := c.Platform.Kind
	if kind == "auto" {
		kind = DetectPlatform()
	}
	mode := "existing"
	switch kind {
	case "keenetic":
	case "linux-systemd":
		mode = c.Platform.Linux.FirewallMode
	case "openwrt":
		mode = c.Platform.OpenWrt.FirewallMode
	default:
		return fmt.Errorf("signed update platform could not be determined")
	}
	if mode == "managed" {
		return fmt.Errorf("signed updates with managed firewall require read-only firewall reconciliation, which is not yet supported")
	}
	if mode != "existing" {
		return fmt.Errorf("signed update firewall mode is unsupported")
	}
	return nil
}
