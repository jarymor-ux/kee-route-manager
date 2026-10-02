package core

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/platform"
	"github.com/jarymor-ux/kee-route-manager/internal/store"
	"github.com/jarymor-ux/kee-route-manager/internal/tunnel"
	"github.com/jarymor-ux/kee-route-manager/internal/xray"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type txRegressionPlatform struct{ fakeAdapter }

func (p *txRegressionPlatform) RestartXray(context.Context) error { return nil }
func (p *txRegressionPlatform) XrayRunning(context.Context) bool  { return true }

type txRegressionRunner struct{}

func (txRegressionRunner) Run(context.Context, []string) ([]byte, error) { return nil, nil }
func TestTransactionReplayPreservesOperatorChanges(t *testing.T) {
	for _, mode := range []string{"base_target", "managed_hash"} {
		for _, pending := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/pending_%v", mode, pending), func(t *testing.T) {
				m, xm, next, node, original := transactionFileFixture(t)
				c, st := m.cfg, m.store
				var err error
				if err = m.commitRoute(context.Background(), next, []model.Node{node}, "pool"); err != nil {
					t.Fatal(err)
				}
				state := st.State()
				if pending {
					next = state
					next.LastSwitchReason = "interrupted selection"
					if err = st.PrepareTransaction(store.Transaction{ID: "pending", Kind: "select", Before: state, Desired: next, Nodes: st.Nodes()}); err != nil {
						t.Fatal(err)
					}
				}
				changedPath := c.Xray.BaseRoutingFile
				if mode == "base_target" {
					if err = os.WriteFile(changedPath, []byte(original), 0600); err != nil {
						t.Fatal(err)
					}
				} else {
					changedPath = filepath.Join(c.Xray.ManagedDir, "03_90_kee_route_manager_inbounds.json")
					data, e := os.ReadFile(changedPath)
					if e != nil {
						t.Fatal(e)
					}
					var root map[string]any
					if e = json.Unmarshal(data, &root); e != nil {
						t.Fatal(e)
					}
					root["operator_marker"] = "preserve-me"
					data, e = json.Marshal(root)
					if e != nil {
						t.Fatal(e)
					}
					if e = os.WriteFile(changedPath, data, 0600); e != nil {
						t.Fatal(e)
					}
				}
				edited, err := os.ReadFile(changedPath)
				if err != nil {
					t.Fatal(err)
				}
				actual, err := xm.ActualState(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("before replay: pending=%v drift=%q hash_changed=%v", pending, actual.Drift, state.XrayConfigHash != actual.ConfigHash)
				err = m.reconcileStartup(context.Background())
				after, e := os.ReadFile(changedPath)
				if e != nil {
					t.Fatal(e)
				}
				if pending {
					if err == nil {
						t.Fatal("pending replay must refuse operator drift")
					}
					if string(after) != string(edited) {
						t.Fatal("pending replay overwrote operator edit")
					}
					tx, e := st.PendingTransaction()
					if e != nil || tx == nil || tx.Stage != store.Prepared {
						t.Fatalf("refusal must retain original journal: %+v %v", tx, e)
					}
				} else {
					if err == nil {
						t.Fatal("normal startup must reject drift")
					}
					if string(after) != string(edited) {
						t.Fatal("negative control changed file")
					}
					t.Logf("NEGATIVE: startup refused and preserved edit: %v", err)
				}
			})
		}
	}
}
func TestActionEventRoundTripWithLargeDiagnostics(t *testing.T) {
	for _, size := range []int{64 << 10, (1 << 20) + 1024} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m, _, _ := fixture(t)
			d := t.TempDir()
			payload := filepath.Join(d, "error.txt")
			script := filepath.Join(d, "restart-fixture.sh")
			if err := os.WriteFile(payload, []byte(strings.Repeat("x", size)), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(script, []byte("#!/bin/sh\ncat \"$1\" >&2\nexit 1\n"), 0700); err != nil {
				t.Fatal(err)
			}
			c := m.cfg
			c.Platform.Kind = "linux-systemd"
			c.Platform.XrayRestartCommand = []string{script, payload}
			p, _, err := platform.New(c)
			if err != nil {
				t.Fatal(err)
			}
			m.platform = p
			err = m.RestartXray(context.Background())
			if err == nil {
				t.Fatal("injected restart error missing")
			}
			t.Logf("action error bytes=%d", len(err.Error()))
			info, err := os.Stat(m.store.Path("events.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("durable event file bytes=%d", info.Size())
			stateDir := filepath.Dir(m.store.Path("state.json"))
			reopened, err := store.New(stateDir, stateDir, model.NewState("rc2", m.cfg.Xray.SlotTagPrefix, 2))
			if err != nil {
				t.Fatalf("persisted diagnostic must reopen: %v", err)
			}
			events := m.store.Events(0, 100)
			if len(events) == 0 {
				t.Fatal("diagnostic event missing")
			}
			last := events[len(events)-1]
			loaded := reopened.Events(0, 100)
			if len(loaded) != len(events) || loaded[len(loaded)-1].Message != last.Message {
				t.Fatal("serialized event does not round trip at bound")
			}
			if size > 1<<20 && !strings.Contains(last.Message, "truncated") {
				t.Fatalf("large diagnostic lacks truncation marker: %d bytes", len(last.Message))
			}
			if size == 64<<10 && !strings.Contains(last.Message, strings.Repeat("x", size)) {
				t.Fatal("bounded control lost diagnostics")
			}
		})
	}
}

func transactionFileFixture(t *testing.T) (*Manager, *xray.Manager, model.State, model.Node, string) {
	t.Helper()
	d := t.TempDir()
	c := config.Default()
	c.Pool.Size = 1
	c.Paths.StateDir = filepath.Join(d, "state")
	c.Paths.CacheDir = filepath.Join(d, "cache")
	c.Paths.RunDir = filepath.Join(d, "run")
	c.Xray.ConfigDir = filepath.Join(d, "xray")
	c.Xray.ManagedDir = c.Xray.ConfigDir
	c.Xray.BaseRoutingFile = filepath.Join(c.Xray.ConfigDir, "05_routing.json")
	for _, dir := range []string{c.Xray.ConfigDir, c.Paths.StateDir, c.Paths.RunDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	original := `{"routing":{"rules":[{"type":"field","inboundTag":["redirect","tproxy"],"outboundTag":"vless-reality"}]}}`
	if err := os.WriteFile(c.Xray.BaseRoutingFile, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	c.Xray.Binary = filepath.Join(d, "xray-fake")
	if err := os.WriteFile(c.Xray.Binary, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	c.Xray.APIAddress = listener.Addr().String()
	st, err := store.New(c.Paths.StateDir, c.Paths.CacheDir, model.NewState("review", c.Xray.SlotTagPrefix, 1))
	if err != nil {
		t.Fatal(err)
	}
	p := &txRegressionPlatform{}
	xm := xray.NewManager(c, txRegressionRunner{}, p)
	m := New(c, "review", st, nil, p, xm, nil, nil)
	node := model.Node{ID: "n", Protocol: "vless", Address: "203.0.113.10", Port: 443, UUID: "12345678-1234-1234-1234-123456789abc", Network: "tcp", Security: "reality", ServerName: "example.com", PublicKey: "synthetic-key"}
	next := st.State()
	next.Pool[0].NodeID = node.ID
	next.ActiveSlot = 0
	next.ActiveNodeID = node.ID
	next.XrayConfigured = true
	next.XrayGeneration = 1

	return m, xm, next, node, original
}

func TestProvenReplayRefusesOperatorChanges(t *testing.T) {
	for _, mode := range []string{"base_target", "managed_hash", "managed_missing", "base_unrelated"} {
		t.Run(mode, func(t *testing.T) {
			m, xm, next, node, original := transactionFileFixture(t)
			if err := m.commitRoute(context.Background(), next, []model.Node{node}, "pool"); err != nil {
				t.Fatal(err)
			}
			state := m.store.State()
			desired := tunnel.DesiredPool{Slots: state.Pool, Nodes: nodeMap(m.store.Nodes()), Selection: m.selection(state)}
			proof, err := xm.PrepareReplay(context.Background(), desired, false)
			if err != nil {
				t.Fatal(err)
			}
			tx := store.Transaction{ID: "proven", Kind: "select", Before: state, Desired: state, Nodes: m.store.Nodes(), Replay: proof}
			if err = m.store.PrepareTransaction(tx); err != nil {
				t.Fatal(err)
			}
			if err = m.store.AdvanceTransaction(store.XrayFilesStaged); err != nil {
				t.Fatal(err)
			}
			path := m.cfg.Xray.BaseRoutingFile
			switch mode {
			case "base_target":
				err = os.WriteFile(path, []byte(original), 0600)
			case "base_unrelated":
				b, e := os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
				var root map[string]any
				if e = json.Unmarshal(b, &root); e != nil {
					t.Fatal(e)
				}
				root["operator_marker"] = "preserve"
				b, _ = json.Marshal(root)
				err = os.WriteFile(path, b, 0600)
			case "managed_hash":
				path = filepath.Join(m.cfg.Xray.ManagedDir, "03_90_kee_route_manager_inbounds.json")
				b, e := os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
				err = os.WriteFile(path, append(b, ' '), 0600)
			case "managed_missing":
				path = filepath.Join(m.cfg.Xray.ManagedDir, "03_90_kee_route_manager_inbounds.json")
				err = os.Remove(path)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, beforeErr := os.ReadFile(path)
			if err = m.reconcileStartup(context.Background()); err == nil {
				t.Fatal("operator drift accepted")
			}
			after, afterErr := os.ReadFile(path)
			if string(before) != string(after) || os.IsNotExist(beforeErr) != os.IsNotExist(afterErr) {
				t.Fatal("operator edit changed during refused replay")
			}
			txp, e := m.store.PendingTransaction()
			if e != nil || txp == nil || txp.Stage != store.XrayFilesStaged {
				t.Fatal("journal restarted before drift guard")
			}
		})
	}
}

func TestReplayAcceptsEveryPartialFileWrite(t *testing.T) {
	for _, kind := range []string{"bootstrap", "pool", "select", "restore"} {
		for cut := 0; cut <= 5; cut++ {
			t.Run(fmt.Sprintf("%s/cut%d", kind, cut), func(t *testing.T) {
				m, xm, next, node, _ := transactionFileFixture(t)
				ctx := context.Background()
				if kind != "bootstrap" {
					if err := m.commitRoute(ctx, next, []model.Node{node}, "pool"); err != nil {
						t.Fatal(err)
					}
					next = m.store.State()
				}
				before := m.store.State()
				// Unrelated operator additions made before restore must survive both
				// expected-output planning and each legitimate partial replay.
				if kind == "restore" {
					path := m.cfg.Xray.BaseRoutingFile
					b, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					var root map[string]any
					if err = json.Unmarshal(b, &root); err != nil {
						t.Fatal(err)
					}
					r := root["routing"].(map[string]any)
					r["domainStrategy"] = "IPIfNonMatch"
					r["rules"] = append(r["rules"].([]any), map[string]any{"outboundTag": "operator-direct", "domain": []string{"example.org"}})
					b, _ = json.Marshal(root)
					if err = os.WriteFile(path, b, 0600); err != nil {
						t.Fatal(err)
					}
					next = model.NewState("review", m.cfg.Xray.SlotTagPrefix, 1)
				} else if kind == "pool" {
					node.ID = "replacement"
					node.Address = "203.0.113.11"
					next.Pool[0].NodeID = node.ID
					next.ActiveNodeID = node.ID
					next.XrayGeneration++
				} else if kind == "select" {
					next.DirectMode = true
					next.ActiveSlot = -1
					next.ActiveNodeID = ""
				}
				nodes := []model.Node{node}
				desired := tunnel.DesiredPool{Previous: before.Pool, Slots: next.Pool, Nodes: nodeMap(nodes), ActiveSlot: next.ActiveSlot, Selection: m.selection(next)}
				proof, err := xm.PrepareReplay(ctx, desired, kind == "restore")
				if err != nil {
					t.Fatal(err)
				}
				paths := []string{}
				for _, name := range []string{"00_90_kee_route_manager_api.json", "03_90_kee_route_manager_inbounds.json", "04_90_kee_route_manager_outbounds.json", "05_90_kee_route_manager_routing.json"} {
					paths = append(paths, filepath.Join(m.cfg.Xray.ManagedDir, name))
				}
				paths = append(paths, m.cfg.Xray.BaseRoutingFile)
				old := map[string][]byte{}
				for _, path := range paths {
					b, e := os.ReadFile(path)
					if e != nil && !os.IsNotExist(e) {
						t.Fatal(e)
					}
					if e == nil {
						old[path] = b
					}
				}
				journalKind := kind
				if kind == "bootstrap" {
					journalKind = "pool"
				}
				tx := store.Transaction{ID: "partial", Kind: journalKind, Before: before, Desired: next, Nodes: nodes, Replay: proof}
				if err = m.store.PrepareTransaction(tx); err != nil {
					t.Fatal(err)
				}
				if kind == "restore" {
					err = xm.Restore(ctx)
				} else if kind == "select" {
					err = xm.Select(ctx, desired.Selection)
				} else {
					err = xm.Bootstrap(ctx, desired)
				}
				if err != nil {
					t.Fatal(err)
				}
				// Atomic per-file writes can leave any mix of the two saved identities.
				for i, path := range paths {
					if i >= cut {
						if b, ok := old[path]; ok {
							err = os.WriteFile(path, b, 0600)
						} else {
							err = os.Remove(path)
							if os.IsNotExist(err) {
								err = nil
							}
						}
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				if err = xm.ValidateReplay(ctx, proof); err != nil {
					t.Fatalf("legitimate partial write rejected: %v", err)
				}
				if err = m.reconcileStartup(ctx); err != nil {
					t.Fatalf("partial replay failed: %v", err)
				}
				if txp, e := m.store.PendingTransaction(); e != nil || txp != nil {
					t.Fatal("replay left pending journal")
				}
				if kind == "restore" {
					b, e := os.ReadFile(m.cfg.Xray.BaseRoutingFile)
					if e != nil {
						t.Fatal(e)
					}
					if !strings.Contains(string(b), "operator-direct") || !strings.Contains(string(b), "IPIfNonMatch") {
						t.Fatal("restore lost operator routing additions")
					}
				} else if m.State().DirectMode != next.DirectMode || m.State().ActiveNodeID != next.ActiveNodeID {
					t.Fatal("desired selection not restored")
				}
			})
		}
	}
}

type txInterruptedRunner struct{ fail bool }

func (r *txInterruptedRunner) Run(_ context.Context, args []string) ([]byte, error) {
	if r.fail && len(args) > 2 && args[1] == "api" && args[2] == "bo" {
		return nil, fmt.Errorf("injected runtime selection failure")
	}
	return nil, nil
}
func TestCommitPersistsReplayProofBeforeFailedSelection(t *testing.T) {
	m, _, next, node, _ := transactionFileFixture(t)
	ctx := context.Background()
	if err := m.commitRoute(ctx, next, []model.Node{node}, "pool"); err != nil {
		t.Fatal(err)
	}
	runner := &txInterruptedRunner{fail: true}
	xm := xray.NewManager(m.cfg, runner, m.platform)
	m.xray = xm
	next = m.store.State()
	next.DirectMode = true
	next.ActiveSlot = -1
	next.ActiveNodeID = ""
	if err := m.commitRoute(ctx, next, m.store.Nodes(), "select"); err == nil {
		t.Fatal("failure injection missing")
	}
	tx, err := m.store.PendingTransaction()
	if err != nil || tx == nil || len(tx.Replay) != 5 {
		t.Fatalf("missing durable pre-effect proof: %+v %v", tx, err)
	}
	runner.fail = false
	if err = m.reconcileStartup(ctx); err != nil {
		t.Fatal(err)
	}
	if !m.State().DirectMode {
		t.Fatal("failed selection was not recovered")
	}
}

func TestLegacyInitialReplayWithoutIdentityRefusesAmbiguity(t *testing.T) {
	m, _, next, node, _ := transactionFileFixture(t)
	tx := store.Transaction{ID: "legacy-initial", Kind: "pool", Before: m.store.State(), Desired: next, Nodes: []model.Node{node}}
	if err := m.store.PrepareTransaction(tx); err != nil {
		t.Fatal(err)
	}
	path := m.cfg.Xray.BaseRoutingFile
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err = json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	root["operator_marker"] = "repaired"
	b, _ = json.Marshal(root)
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if err = m.reconcileStartup(context.Background()); err == nil {
		t.Fatal("ambiguous legacy initial replay must require operator reconciliation")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(b) {
		t.Fatal("legacy ambiguity changed operator file")
	}
	if pending, e := m.store.PendingTransaction(); e != nil || pending == nil || pending.Stage != store.Prepared {
		t.Fatal("legacy refusal restarted journal")
	}
}

func TestLegacyReplayWithMatchingIdentity(t *testing.T) {
	m, _, next, node, _ := transactionFileFixture(t)
	ctx := context.Background()
	if err := m.commitRoute(ctx, next, []model.Node{node}, "pool"); err != nil {
		t.Fatal(err)
	}
	before := m.store.State()
	next = before
	next.DirectMode = true
	next.ActiveSlot = -1
	next.ActiveNodeID = ""
	if err := m.store.PrepareTransaction(store.Transaction{ID: "legacy-control", Kind: "select", Before: before, Desired: next, Nodes: m.store.Nodes()}); err != nil {
		t.Fatal(err)
	}
	if err := m.reconcileStartup(ctx); err != nil {
		t.Fatalf("matching legacy identity refused: %v", err)
	}
	if !m.State().DirectMode {
		t.Fatal("legacy control did not finish selection")
	}
}
