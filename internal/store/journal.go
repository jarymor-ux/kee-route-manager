package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/tunnel"
	"os"
)

const (
	Prepared           = "PREPARED"
	XrayFilesStaged    = "XRAY_FILES_STAGED"
	XrayRuntimeApplied = "XRAY_RUNTIME_APPLIED"
	FirewallApplied    = "FIREWALL_APPLIED"
	SelectionApplied   = "SELECTION_APPLIED"
	StateCommitted     = "STATE_COMMITTED"
	Done               = "DONE"
)

type Transaction struct {
	Schema  int                `json:"schema"`
	ID      string             `json:"id"`
	Stage   string             `json:"stage"`
	Kind    string             `json:"kind"`
	Before  model.State        `json:"before"`
	Desired model.State        `json:"desired"`
	Nodes   []model.Node       `json:"nodes"`
	Replay  tunnel.ReplayProof `json:"replay,omitempty"`
}

// Journal is a single-owner write-ahead record; the daemon process lock and route
// mutation lock must be held while advancing it. Recovery reapplies desired state.
func (s *Store) PrepareTransaction(tx Transaction) error {
	pending, err := s.PendingTransaction()
	if err != nil {
		return err
	}
	if pending != nil {
		return fmt.Errorf("unfinished routing transaction requires reconciliation")
	}
	tx.Schema = 1
	tx.Stage = Prepared
	if err = s.validateTransaction(tx); err != nil {
		return err
	}
	return writeJSON(s.Path("transaction.json"), tx)
}
func (s *Store) AdvanceTransaction(stage string) error {
	tx, err := s.PendingTransaction()
	if err != nil {
		return err
	}
	if tx == nil {
		return fmt.Errorf("no pending routing transaction")
	}
	stages := []string{Prepared, XrayFilesStaged, XrayRuntimeApplied, FirewallApplied, SelectionApplied, StateCommitted, Done}
	current, next := -1, -1
	for i, value := range stages {
		if value == tx.Stage {
			current = i
		}
		if value == stage {
			next = i
		}
	}
	if next != current+1 {
		return fmt.Errorf("invalid transaction transition %s -> %s", tx.Stage, stage)
	}
	tx.Stage = stage
	return writeJSON(s.Path("transaction.json"), tx)
}
func (s *Store) PendingTransaction() (*Transaction, error) {
	tx, err := s.readTransaction()
	if err != nil || tx == nil || tx.Stage == Done {
		return nil, err
	}
	return tx, nil
}

func (s *Store) readTransaction() (*Transaction, error) {
	b, err := os.ReadFile(s.Path("transaction.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var tx Transaction
	if err = json.Unmarshal(b, &tx); err != nil {
		return nil, fmt.Errorf("corrupt transaction journal: %w", err)
	}
	if tx.Schema != 1 || tx.ID == "" {
		return nil, fmt.Errorf("invalid transaction journal")
	}
	stages := map[string]bool{Prepared: true, XrayFilesStaged: true, XrayRuntimeApplied: true, FirewallApplied: true, SelectionApplied: true, StateCommitted: true, Done: true}
	if !stages[tx.Stage] {
		return nil, fmt.Errorf("invalid transaction stage")
	}
	if err = s.validateTransaction(tx); err != nil {
		return nil, err
	}
	return &tx, nil
}

// RestartTransaction allows an interrupted idempotent forward repair to replay
// from PREPARED after checking actual tunnel/firewall state.
func (s *Store) RestartTransaction(tx Transaction) error {
	tx.Stage = Prepared
	if err := s.validateTransaction(tx); err != nil {
		return err
	}
	return writeJSON(s.Path("transaction.json"), tx)
}

func (s *Store) validateTransaction(tx Transaction) error {
	if tx.Schema != 1 || tx.ID == "" {
		return fmt.Errorf("invalid transaction journal")
	}
	if tx.Kind != "pool" && tx.Kind != "select" && tx.Kind != "restore" && tx.Kind != "reconcile" {
		return fmt.Errorf("invalid transaction kind")
	}
	nodes := map[string]model.Node{}
	for _, node := range tx.Nodes {
		if node.ID == "" {
			return fmt.Errorf("transaction contains invalid node")
		}
		if _, exists := nodes[node.ID]; exists {
			return fmt.Errorf("duplicate transaction node")
		}
		nodes[node.ID] = node
	}
	if err := s.validateWithNodes(tx.Desired, nodes); err != nil {
		return fmt.Errorf("invalid transaction desired state: %w", err)
	}
	if tx.Kind != "restore" && tx.Desired.XrayGeneration < tx.Before.XrayGeneration {
		return fmt.Errorf("transaction generation regressed")
	}
	return nil
}
