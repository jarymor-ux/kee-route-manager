package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jarymor-ux/kee-route-manager/internal/model"
)

// InspectReadOnly validates the primary state and its node cache without creating
// directories, recovering previous state, sanitizing files, or advancing a journal.
// The caller must hold the daemon's normal state and tunnel ownership locks.
func InspectReadOnly(stateDir, cacheDir string, initial model.State) (model.State, error) {
	var state model.State
	if err := readStrictFile(filepath.Join(stateDir, "state.json"), &state); err != nil {
		return state, err
	}
	var nodes []model.Node
	if err := readStrictFile(filepath.Join(cacheDir, "nodes.json"), &nodes); err != nil {
		return state, err
	}
	index := make(map[string]model.Node, len(nodes))
	for _, node := range nodes {
		if node.ID == "" {
			return state, fmt.Errorf("node cache contains an empty identity")
		}
		if _, exists := index[node.ID]; exists {
			return state, fmt.Errorf("node cache contains duplicate identities")
		}
		index[node.ID] = node
	}
	st := &Store{initial: initial, nodes: index}
	if err := st.validateWithNodes(state, index); err != nil {
		return state, err
	}
	var tx Transaction
	err := readStrictFile(filepath.Join(stateDir, "transaction.json"), &tx)
	if errors.Is(err, os.ErrNotExist) && !state.XrayConfigured {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err = st.validateTransaction(tx); err != nil {
		return state, err
	}
	if tx.Stage != Done {
		return state, fmt.Errorf("routing transaction is not complete")
	}
	// Health/source timestamps can advance outside routing transactions; the
	// committed routing identity must still be exactly the journal's result.
	// XrayConfigHash is finalized after effects in the primary state; the journal
	// retains the earlier intent. The trial compares that final hash to real files.
	want := tx.Desired
	if state.XrayConfigured != want.XrayConfigured || state.XrayGeneration != want.XrayGeneration ||
		state.DirectMode != want.DirectMode ||
		state.ActiveSlot != want.ActiveSlot || state.ActiveNodeID != want.ActiveNodeID ||
		state.AutomaticRoutingPaused != want.AutomaticRoutingPaused {
		return state, fmt.Errorf("primary routing state differs from completed journal")
	}
	return state, nil
}

func readStrictFile(path string, into any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return fmt.Errorf("invalid %s file", filepath.Base(path))
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, (64<<20)+1))
	d.DisallowUnknownFields()
	if err = d.Decode(into); err != nil {
		return fmt.Errorf("invalid %s JSON", filepath.Base(path))
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing %s JSON", filepath.Base(path))
	}
	return nil
}
