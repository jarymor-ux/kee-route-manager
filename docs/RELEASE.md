# RC2 release procedure

RC1 tag/assets are immutable. Work occurs on `release/1.0.0-rc.2`; publish a separate **prerelease**, never mark latest stable. The owner deferred real Keenetic acceptance; keep every hardware row pending and all platforms experimental until a report exists.

1. Run source/race/vet/Staticcheck/Govulncheck/ShellCheck/JS/fuzz, signed bootstrap tamper tests, Linux integration and all component cross-builds. CI must be green for the exact merged commit.
2. Push topic branch, create PR, review/integrate, merge main. Create signed tag only after software gates. Do not change existing tags.
3. Keep Ed25519 signing key outside Git, mode0600. RC2 public key rotation must match templates/bootstrap; RC1 key/assets are unchanged.
4. Build signed artifacts from final commit:

```sh
KRM_RELEASE_PRIVATE_KEY=/secure/external/rc2-release.private.key ./scripts/build-release.sh
python3 scripts/verify-release.py release/dist
```

All daemon/UI/ctl/release-tool binaries are built for amd64/arm64/armv7/mipsle. Build tool executes native host signing tool, never attempts to execute foreign release binaries. Artifacts: component binaries, three bootstraps, signed install payload, manifest/signature, SHA256SUMS/signature, SPDX inventory.

5. Check binary sizes, native Linux version/validation/readiness/HTTP assets and signed artifacts. Publish release notes with exact unresolved hardware/capability limits. Attach SHA256SUMS.
6. Complete [hardware plan](HARDWARE_TEST_PLAN.md) separately with router access. A stable release requires acceptance; RC2 code/tests alone do not close hardware DoD.

`./scripts/test-release.sh` creates a disposable external signing fixture and verifies all digests plus deliberate tamper rejection without changing production trust key.
