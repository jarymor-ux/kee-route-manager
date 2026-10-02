package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestXrayRejectsNestedConfdirResources(t *testing.T) {
	for _, resource := range []string{"managed", "routing"} {
		t.Run(resource, func(t *testing.T) {
			c := validConfig(t)
			root := t.TempDir()
			c.Xray.ConfigDir = root
			c.Xray.ManagedDir = root
			c.Xray.BaseRoutingFile = filepath.Join(root, "routing.json")
			if resource == "managed" {
				c.Xray.ManagedDir = filepath.Join(root, "nested")
			} else {
				c.Xray.BaseRoutingFile = filepath.Join(root, "nested", "routing.json")
			}
			if err := c.Validate(); err == nil {
				t.Fatal("nested resource accepted although Xray -confdir does not recurse")
			}
		})
	}
}

func TestXrayAcceptsSamePhysicalConfdirAliases(t *testing.T) {
	c := validConfig(t)
	root := t.TempDir()
	real := filepath.Join(root, "real")
	alias := filepath.Join(root, "alias")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	c.Xray.ConfigDir = alias
	c.Xray.ManagedDir = real
	c.Xray.BaseRoutingFile = filepath.Join(real, "routing.json")
	if err := c.Validate(); err != nil {
		t.Fatalf("same physical directory refused: %v", err)
	}
}
