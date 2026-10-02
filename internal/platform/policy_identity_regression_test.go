package platform

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPolicyRuleIdentityDriftRefusedBeforeEffects(t *testing.T) {
	cases := map[string]string{
		"source":      `{"priority":10000,"table":100,"fwmark":255,"src":"192.0.2.0","srclen":24}`,
		"source_host": `{"priority":10000,"table":100,"fwmark":255,"src":"0.0.0.0"}`,
		"destination": `{"priority":10000,"table":100,"fwmark":255,"dst":"192.0.2.0/24"}`,
		"mask":        `{"priority":10000,"table":100,"fwmark":255,"fwmask":255}`,
		"invert":      `{"priority":10000,"table":100,"fwmark":255,"not":true}`,
		"iif":         `{"priority":10000,"table":100,"fwmark":255,"iif":"eth9"}`,
		"oif":         `{"priority":10000,"table":100,"fwmark":255,"oif":"eth9"}`,
		"priority":    `{"priority":10001,"table":100,"fwmark":255}`,
		"suppress":    `{"priority":10000,"table":100,"fwmark":255,"suppress_prefixlength":0}`,
		"unexpected":  `{"priority":10000,"table":100,"fwmark":255,"future_selector":true}`,
		"duplicate":   `{"priority":10000,"table":100,"fwmark":255},{"priority":10000,"table":100,"fwmark":255}`,
		"legacy":      `{"priority":10000,"table":100,"fwmark":255}`,
	}
	for name, rule := range cases {
		for _, operation := range []string{"ensure", "remove"} {
			t.Run(name+"/"+operation, func(t *testing.T) {
				g, d := fakeFirewall(t)
				if err := g.EnsureFirewall(context.Background()); err != nil {
					t.Fatal(err)
				}
				owner := `{"mark":255,"table":100,"priority":10000}`
				if name == "legacy" {
					owner = `{"mark":255,"table":100}`
				}
				if err := os.WriteFile(g.ownershipPath(), []byte(owner), 0600); err != nil {
					t.Fatal(err)
				}
				script := `#!/bin/sh
printf '%s\n' "$*" >> "$KRM_TEST_ROOT/ip.log"
case "$*" in
 '-j rule show') printf '%s\n' "$KRM_REGRESSION_RULES" ;;
 '-j route show table 100') printf '[{"type":"local","dst":"default","dev":"lo"}]\n' ;;
 *) exit 0 ;;
esac
`
				if err := os.WriteFile(filepath.Join(d, "bin", "ip"), []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("KRM_REGRESSION_RULES", "["+rule+"]")
				for _, file := range []string{"ip.log", "nft.log"} {
					if err := os.WriteFile(filepath.Join(d, file), nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
				var err error
				if operation == "ensure" {
					err = g.EnsureFirewall(context.Background())
				} else {
					err = g.RemoveFirewall(context.Background())
				}
				if err == nil {
					t.Fatal("drifted rule accepted")
				}
				for _, file := range []string{"ip.log", "nft.log"} {
					b, _ := os.ReadFile(filepath.Join(d, file))
					for _, mutation := range []string{"route replace", "route del", "rule add", "rule del", "delete table", "-f "} {
						if strings.Contains(string(b), mutation) {
							t.Fatalf("mutation before drift rejection: %s", b)
						}
					}
				}
			})
		}
	}
}

func TestPolicyRuleIdentityNormalizationControl(t *testing.T) {
	g, d := fakeFirewall(t)
	if err := g.EnsureFirewall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(g.ownershipPath(), []byte(`{"mark":255,"table":100,"priority":10000}`), 0600); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$KRM_TEST_ROOT/ip.log"
case "$*" in
 '-j rule show') printf '[{"priority":10000,"table":"100","fwmark":"0xff","fwmask":"0xffffffff","src":"all","srclen":0,"dst":"0.0.0.0/0","dstlen":0}]\n' ;;
 '-j route show table 100') printf '[{"type":"local","dst":"default","dev":"lo"}]\n' ;;
 *) exit 0 ;;
esac
`
	if err := os.WriteFile(filepath.Join(d, "bin", "ip"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := g.EnsureFirewall(context.Background()); err != nil {
		t.Fatalf("equivalent rule rejected: %v", err)
	}
	if err := g.RemoveFirewall(context.Background()); err != nil {
		t.Fatalf("unchanged rule cleanup rejected: %v", err)
	}
}

func TestLegacyOwnershipWithAbsentRuleCanMigrateControl(t *testing.T) {
	g, d := fakeFirewall(t)
	if err := g.EnsureFirewall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(d, "policy")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(g.ownershipPath(), []byte(`{"mark":255,"table":100}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := g.EnsureFirewall(context.Background()); err != nil {
		t.Fatalf("absent legacy rule cannot safely migrate: %v", err)
	}
	owner, owned, err := g.ownership()
	if err != nil || !owned || owner.Priority != managedRulePriority {
		t.Fatalf("explicit identity was not persisted: %+v %v", owner, err)
	}
	if err = g.RemoveFirewall(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyIdentityLargeNumbersControl(t *testing.T) {
	c := firewallConfig{Mark: 1 << 30, Table: 1 << 30}
	rule := map[string]any{"priority": float64(managedRulePriority), "table": float64(c.Table), "fwmark": float64(c.Mark), "src": "all"}
	if !managedPolicyIdentity(rule, c, managedRulePriority) {
		t.Fatal("valid JSON large numeric mark/table rejected")
	}
}
