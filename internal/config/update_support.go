package config

import "fmt"

// UpdateApplySupport rejects unsupported controller platforms before staging or effects.
func (c Config) UpdateApplySupport() error {
	kind := c.Platform.Kind
	if kind == "auto" {
		kind = DetectPlatform()
	}
	if kind != "keenetic" {
		return fmt.Errorf("signed updates require the Keenetic/Netcraze platform")
	}
	return nil
}
