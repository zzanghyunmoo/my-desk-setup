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
  --output target-evidence \
  --profile owner

scripts/verify-target-evidence.sh \
  --bundle target-evidence \
  --expected-cli-revision '0.1.0 (commit=<commit>, date=<date>)' \
  --expected-catalog-revision 'sha256:<catalog>' \
  --expected-plan-digest 'sha256:<plan>' \
  --expected-target lima-guest:mds \
  --require-verified
```

Authentication remains user-owned. Neither command accepts auth/login/token
arguments, and the verifier rejects credential-shaped content, auth commands,
credential flags, personal home paths, symlinks/reparse points, checksum
mismatches, and extra files.
