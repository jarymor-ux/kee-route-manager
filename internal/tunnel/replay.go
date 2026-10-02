package tunnel

import "context"

// ReplayFile records the only file identities an interrupted operation can leave.
// Empty hashes mean absent files; atomic replacement cannot leave partial bytes.
type ReplayFile struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

type ReplayProof map[string]ReplayFile

// ReplayGuard is implemented by persistent backends that can distinguish their
// interrupted writes from operator edits before any recovery mutation.
type ReplayGuard interface {
	PrepareReplay(context.Context, DesiredPool, bool) (ReplayProof, error)
	ValidateReplay(context.Context, ReplayProof) error
}
