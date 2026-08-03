# Target evidence test boundary

`go test ./internal/evidence` creates local fixture bundles only to prove the
`mds.target-evidence/v2` contract and its tamper gates. A fixture may exercise
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
scripts/prepare-target-certification.sh \
  --mds-evidence /usr/local/bin/mds-evidence \
  --expected-mds-evidence-sha256 '<release-certifier-sha256>' \
  --mds /usr/local/bin/mds \
  --target lima-guest:mds \
  --expected-binary-sha256 '<release-binary-sha256>' \
  --profile certification-lima-guest > preparation.json

scripts/certify-target.sh \
  --mds-evidence /usr/local/bin/mds-evidence \
  --expected-mds-evidence-sha256 '<release-certifier-sha256>' \
  --mds /usr/local/bin/mds \
  --target lima-guest:mds \
  --output target-evidence/run-manual-001 \
  --cohort 'cert-20260731T120000Z-<commit8>' \
  --expected-binary-sha256 '<release-binary-sha256>' \
  --expected-plan-digest 'sha256:<reviewed-plan>' \
  --expected-guest-creation-nonce-commitment 'sha256:<host-reviewed-commitment>' \
  --profile certification-lima-guest

scripts/verify-target-evidence.sh \
  --mds-evidence /usr/local/bin/mds-evidence \
  --expected-mds-evidence-sha256 '<release-certifier-sha256>' \
  --bundle target-evidence/run-manual-001 \
  --expected-cli-revision '0.1.0 (commit=<commit>, date=<date>)' \
  --expected-catalog-revision 'sha256:<catalog>' \
  --expected-plan-digest 'sha256:<target-eligible-all-plan>' \
  --expected-target lima-guest:mds \
  --expected-binary-sha256 '<release-binary-sha256>' \
  --expected-cohort 'cert-20260731T120000Z-<commit8>' \
  --max-age 45m \
  --require-verified
```

For a manual WSL/Lima run, compare the host's committed ownership record with
the live marker and pass only the domain-separated commitment emitted by the
host plan. The v3 guest marker contains only that public commitment; the raw
nonce remains in the owner-only host record. The released `mds-evidence prepare`
observes the marker commitment and emits the exact plan digest without mutation. The self-hosted
workflow accepts that non-secret commitment, but never the raw nonce or a
machine-local binary path. Host and guest runner services must not define a raw
nonce environment variable.

The workflow maps each exact target ID to one target-specific certification
profile and runner label. The mapping is closed: dispatchers cannot supply a
profile, and an unknown target ID fails before capture. General `all` and
`owner` selection keep their honest manual and platform-limited outcomes.

The workflow appends the GitHub run ID and attempt to its target-local output
parent. Artifact names bind target kind, expected commit, immutable cohort,
run ID, and attempt. Only a bundle that passes strict `--require-verified`,
Gitleaks, and raw-nonce-field verification is uploaded. An honestly `blocked`
capture remains runner-local for diagnosis. Missing, stale, cross-cohort,
duplicate, unsupported, planned unready/conflict, wrong-target, and wrong-binary
evidence remain failures.

Authentication remains user-owned. Neither command accepts auth/login/token
arguments, and the verifier rejects credential-shaped content, auth commands,
credential flags, personal home paths, symlinks/reparse points, checksum
mismatches, and extra files.
