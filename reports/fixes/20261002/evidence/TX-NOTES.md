# TX-01 and TX-02 fix handoff

Verdicts: both confirmed via adapted regressions against pre-fix source; original reports untouched.

## TX-01

Production files: internal/core/transactions.go, internal/store/journal.go, internal/tunnel/replay.go, internal/xray/replay.go. Regression file: internal/core/transactions_regression_test.go.

Journal intent now carries optional per-file before/final SHA-256 identities (empty string means absence). Xray prepares identities using BuildManaged, existing persistent-selection transform, and existing base-routing patch/reverse functions on private temporary files before intent persistence. Both managed fragments and base routing are covered. Recovery validates the complete configured path set against before/final identities before RestartTransaction or effects; exact bytes must match one legitimate atomic-write endpoint. Drift retains files and the journal stage. Legacy journals reject observed adopted-rule drift, prior managed hash mismatch and missing configured fragments. Persistent backends also refuse legacy initial journals lacking any verifiable before identity. Matching legacy managed identities still recover.

Fresh controls cover actual production Xray files with fake validation/restart/API effects: ordinary/legacy startup protects adopted targets and managed hash edits; proof recovery refuses changed adopted target, unrelated base edit, managed hash edit, and unexpectedly missing fragment. 24 controls cover bootstrap/pool/select/restore at 6 file cut positions, including all missing/already-written fragments, actual Select before/after effects and already-reversed base routing. Restore preserves pre-intent operator-added rules/domainStrategy. Real commit failure persists a full proof before failed runtime selection; subsequent recovery finishes. Legacy initial no-identity refusal keeps PREPARED journal; matching identity negative control completes.

Pre-fix command (exit 1): go test -count=1 -v ./internal/core -run 'Test(TransactionReplayPreservesOperatorChanges|ActionEventRoundTripWithLargeDiagnostics)$'; /tmp/krm-fix-evidence/tx-before.log. Both pending drift classes failed; ordinary startup controls passed.
Additional legacy regression before guard (exit 1): go test -count=1 -v ./internal/core -run '^TestLegacyInitialReplayWithoutIdentityRefusesAmbiguity$'; tx01-legacy-before.log. Same command after guard exit 0; tx01-legacy-after.log.

Limits: hashes validate byte identities, not hardware durability. Ambiguous legacy initial/partially changed transactions require operator reconciliation and retain their journal. A new operator edit after journal intent (including unrelated base formatting/fields) is conservatively refused and preserved. Concurrent foreign writers during the actual effect window remain outside the sole-owner lock contract. No hardware/Xray process API/power-loss qualification.

## TX-02

Production files: internal/store/store.go, internal/store/event_limits.go. Regressions: internal/store/event_limits_test.go and production command/action fixture in internal/core/transactions_regression_test.go.

Event writer enforces serialized JSON length <= 1 MiB minus 1 byte before disk append (newline additionally occupies 1 byte). Oversized diagnostics retain an explicit truncation marker; fields and oversized metadata are clipped/omitted to keep the complete serialized event bounded, including JSON escapes. Bounded events remain intact. Compaction uses the same serializer. Startup streams through a fixed 64 KiB Reader, retains at most the bounded prefix of one line, drains larger legacy lines, emits an explicit omission marker and recovers the old KRM prefix sequence. Surrounding records and routing state remain recoverable, including a final oversized record sequence. New maximum-bound events round-trip exactly.

Pre-fix production command/action/reopen: tx-before.log (oversized reopen failed; 64 KiB control passed). Store regressions before fix (exit 1): go test -count=1 -v ./internal/store -run 'Test(LegacyOversizedEventDoesNotBlockStateRecovery|EventSerializedBoundIncludesFieldsAndEscaping)$'; tx02-store-before.log. Both failed scanner bound. After fix targeted core/store command exit 0: go test -count=1 -v ./internal/core ./internal/store -run 'Test(TransactionReplayPreservesOperatorChanges|ActionEventRoundTripWithLargeDiagnostics|LegacyOversizedEventDoesNotBlockStateRecovery|EventSerializedBoundIncludesFieldsAndEscaping)$'; tx-after.log.

Limits: oversized legacy event diagnostic content is omitted with marker; its sequence is recovered from ordinary old KRM field ordering. No claim of recovering arbitrary malformed/reordered oversized foreign JSON or preserving all oversized diagnostic fields. Event record memory is bounded independently of legacy line length; full retained history still follows existing maxEvents/maxEventLogSize policy.

## Final consolidated bounded verification

go test -race -count=1 -v ./internal/core ./internal/store ./internal/xray ./internal/tunnel
Exit 0; tx-race-final.log. Core 45.465s, store 17.602s, xray 3.981s; tunnel has no direct test files. Includes existing six-stage subprocess SIGKILL fake-adapter replay, root R/Xray changes, all adapted TX fixtures and controls. No race reported. An earlier tx-race.log also passed but predates last legacy guard/control; final log is authoritative.

go vet ./internal/core ./internal/store ./internal/xray ./internal/tunnel
See tx-vet.log; final exit reported to parent separately.

Integration review: bounded reads of root R-01/02/03 and AUTH-01 changes found no new issue. R-01 retains active unless replacement authorized and remaps larger-pool winner; R-02 pins healthy recovery path and resets absent/failed candidates; R-03 budgets independent WAN confirmation and final effects. AUTH-01 socket-peer UI quotas ignore arbitrary forwarded headers. Nonloopback core-upstream concern rejected by enforced loopback api.listen and documented authenticated SSH tunnel remote-UI deployment. NAT/extra proxy peer merging remains a transport limitation.
