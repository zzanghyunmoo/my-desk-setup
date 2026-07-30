# Target evidence test boundary

`go test ./internal/evidence` creates local fixture bundles only to prove the
`mds.target-evidence/v1` contract and its tamper gates. A fixture may exercise
the `blocked` and `verified` validators, but the hosted fixture lane itself has
status `implemented`; its temporary bundles are not actual-machine
certification and must never be published as `verified`.

Actual certification runs the reviewed production `mds` binary on the explicit
target. It executes a read-only `plan --format json`, applies that exact digest,
repeats the apply to prove every action converges as a no-op, and then executes
`doctor --format json`. The two receipts are embedded in the exact four-file
bundle, and status is derived as follows:

- `verified`: the first apply is complete, the repeated apply is entirely
  no-op, the plan has no blockers, and every bounded doctor check is ready.
- `blocked`: an apply is incomplete, a plan blocker remains, or any bounded
  doctor check is not ready.
- `implemented`: evidence code and fixtures exist, but no actual target has run.

Use the self-hosted `Actual target` lane in
`.github/workflows/target-certification.yml`, or run:

```bash
scripts/certify-target.sh \
  --mds /absolute/path/to/mds \
  --target lima-guest:mds \
  --output target-evidence/run-manual-001 \
  --expected-binary-sha256 '<release-binary-sha256>' \
  --expected-guest-creation-nonce '<host-ownership-creation-nonce>' \
  --all

scripts/verify-target-evidence.sh \
  --bundle target-evidence/run-manual-001 \
  --expected-cli-revision '0.1.0 (commit=<commit>, date=<date>)' \
  --expected-catalog-revision 'sha256:<catalog>' \
  --expected-plan-digest 'sha256:<target-eligible-all-plan>' \
  --expected-target lima-guest:mds \
  --expected-binary-sha256 '<release-binary-sha256>' \
  --max-age 45m \
  --require-publication-acceptable
```

For a manual WSL/Lima run, read the expected nonce from the host's committed
ownership record and confirm the provider/name before passing it. The
self-hosted workflow does not accept this value from the dispatcher: its
dedicated guest runner service must inherit the target-specific, root-owned
`MDS_EXPECTED_GUEST_CREATION_NONCE`. Host certification omits the nonce flag
and host runner services must not define that environment variable.

The workflow appends the GitHub run ID and attempt to its target-local output
parent. Artifact names bind target kind, expected commit, run ID, and attempt.
Capture may return non-zero for an honestly blocked manual action, but strict
verification must still pass. Missing, stale, duplicate, unsupported, planned
unready/conflict, wrong-target, and wrong-binary evidence remain failures while
artifact upload preserves the attempted result for diagnosis.

Authentication remains user-owned. Neither command accepts auth/login/token
arguments, and the verifier rejects credential-shaped content, auth commands,
credential flags, personal home paths, symlinks/reparse points, checksum
mismatches, and extra files.
