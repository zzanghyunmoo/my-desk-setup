# Target evidence test boundary

`go test ./internal/evidence` creates local fixture bundles only to prove the
`mds.target-evidence/v1` contract and its tamper gates. A fixture may exercise
the `blocked` and `verified` validators, but the hosted fixture lane itself has
status `implemented`; its temporary bundles are not actual-machine
certification and must never be published as `verified`.

Actual certification runs the reviewed production `mds` binary on the explicit
target. It executes only `plan --format json` and `doctor --format json`, writes
the exact four-file bundle, and derives status as follows:

- `verified`: the plan has no blockers and every bounded doctor check is ready.
- `blocked`: a plan blocker or any non-ready doctor check remains.
- `implemented`: evidence code and fixtures exist, but no actual target has run.

Use the self-hosted `Actual target` lane in
`.github/workflows/target-certification.yml`, or run:

```bash
scripts/certify-target.sh \
  --mds /absolute/path/to/mds \
  --target lima-guest:mds \
  --output target-evidence/run-manual-001 \
  --expected-binary-sha256 '<release-binary-sha256>' \
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
