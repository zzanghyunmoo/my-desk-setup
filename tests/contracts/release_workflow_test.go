package contracts_test

import (
	"archive/zip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestCertificationClockUsesGitHubTimeAndFailsClosed(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is required for the certification clock contract")
	}
	for _, test := range []struct {
		name        string
		arguments   []string
		localEpoch  string
		serverEpoch string
		dateHeader  string
		want        string
		wantError   bool
	}{
		{
			name:       "exact sixty second boundary",
			arguments:  []string{"verify"},
			localEpoch: "1000", serverEpoch: "1060",
			dateHeader: "Thu, 31 Jul 2026 12:00:00 GMT",
			want:       "within 60 seconds",
		},
		{
			name:       "clock skew fails",
			arguments:  []string{"verify"},
			localEpoch: "1000", serverEpoch: "1061",
			dateHeader: "Thu, 31 Jul 2026 12:00:00 GMT",
			wantError:  true,
		},
		{
			name:       "missing server date fails",
			arguments:  []string{"verify"},
			localEpoch: "1000", serverEpoch: "1000",
			wantError: true,
		},
		{
			name: "cohort uses server time",
			arguments: []string{
				"cohort",
				"0123456789abcdef0123456789abcdef01234567",
			},
			localEpoch: "1000", serverEpoch: "1000",
			dateHeader: "Thu, 31 Jul 2026 12:00:00 GMT",
			want:       "cert-20260731T120000Z-01234567",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, err := runCertificationClockContract(
				t,
				test.arguments,
				test.localEpoch,
				test.serverEpoch,
				test.dateHeader,
			)
			if test.wantError {
				if err == nil {
					t.Fatalf("clock command succeeded:\n%s", output)
				}
				return
			}
			if err != nil || !strings.Contains(output, test.want) {
				t.Fatalf(
					"clock command error=%v output=%q, want %q",
					err,
					output,
					test.want,
				)
			}
		})
	}
}

func runCertificationClockContract(
	t *testing.T,
	arguments []string,
	localEpoch,
	serverEpoch,
	dateHeader string,
) (string, error) {
	t.Helper()
	root := repositoryRoot(t)
	fixture := t.TempDir()
	bin := filepath.Join(fixture, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(bin, "curl"), `#!/bin/sh
printf 'HTTP/2 200 OK\n'
if [ -n "${MDS_FAKE_DATE_HEADER:-}" ]; then
  printf 'Date: %s\r\n' "$MDS_FAKE_DATE_HEADER"
fi
printf '\n'
`)
	writeExecutable(t, filepath.Join(bin, "date"), `#!/bin/sh
if [ "$#" -eq 2 ] && [ "$1" = -u ] && [ "$2" = +%s ]; then
  printf '%s\n' "$MDS_FAKE_LOCAL_EPOCH"
  exit 0
fi
if [ "$#" -ge 4 ] && [ "$1" = -u ] && [ "$2" = -d ]; then
  printf '%s\n' "$MDS_FAKE_SERVER_EPOCH"
  exit 0
fi
if [ "$#" -ge 4 ] && [ "$1" = -u ] && [ "$2" = -r ]; then
  printf '20260731T120000Z\n'
  exit 0
fi
exit 2
`)
	command := bashFixtureCommand(
		root,
		bin,
		filepath.Join(root, "scripts", "certification-clock.sh"),
		arguments...,
	)
	command.Env = environmentWith(map[string]string{
		"MDS_FAKE_LOCAL_EPOCH":  localEpoch,
		"MDS_FAKE_SERVER_EPOCH": serverEpoch,
		"MDS_FAKE_DATE_HEADER":  dateHeader,
	})
	output, err := command.CombinedOutput()
	return string(output), err
}

func TestReleasePublishRequiresActualTargetPromotion(t *testing.T) {
	root := repositoryRoot(t)
	content, err := os.ReadFile(
		filepath.Join(root, ".github", "workflows", "release.yml"),
	)
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	workflow := string(content)
	for _, required := range []string{
		"promotion:",
		"scripts/promote-release.sh",
		"scripts/verify-promotion-report.sh",
		"release-promotion.json",
		"- promotion",
		"actions: read",
		"Certification-Cohort:",
		"github.ref_protected == true",
		"scripts/publish-release.sh",
		"MDS_CERTIFICATION_COHORT:",
		"MDS_EVIDENCE_ARCHIVE_DIR:",
		"promotion-assets/evidence",
		"Scan durable promotion assets",
		"release-promotion-${{ needs.build.outputs.commit }}-${{ needs.build.outputs.cohort }}",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("release workflow missing %q", required)
		}
	}
	if strings.Contains(workflow, "tests/target-evidence") {
		t.Fatal("release workflow scans fixture evidence as publication input")
	}
}

func TestTargetCertificationCarriesExactPromotionIdentity(t *testing.T) {
	root := repositoryRoot(t)
	content, err := os.ReadFile(
		filepath.Join(root, ".github", "workflows", "target-certification.yml"),
	)
	if err != nil {
		t.Fatalf("read target certification workflow: %v", err)
	}
	workflow := string(content)
	for _, required := range []string{
		"expected_binary_sha256:",
		"cohort:",
		"--expected-binary-sha256",
		"--expected-plan-digest",
		`--cohort "$CERTIFICATION_COHORT"`,
		`--expected-cohort "$CERTIFICATION_COHORT"`,
		"MDS_EXPECTED_GUEST_CREATION_NONCE",
		`[[ ! "${MDS_EXPECTED_GUEST_CREATION_NONCE:-}" =~ ^[0-9a-f]{64}$ ]]`,
		`[[ "${WSL_DISTRO_NAME:-}" != Ubuntu-26.04 ]]`,
		`[[ "${LIMA_INSTANCE:-}" != mds ]]`,
		"--require-verified",
		`--profile "$CERTIFICATION_PROFILE"`,
		"continue-on-error: true",
		"github.event.repository.fork == false",
		"github.ref_protected == true",
		"ref: ${{ github.sha }}",
		"persist-credentials: false",
		"clean: true",
		"EXPECTED_COMMIT: ${{ github.sha }}",
		`test "$(git rev-parse HEAD)" = "$EXPECTED_COMMIT"`,
		`test -z "$(git status --porcelain=v1 --untracked-files=all)"`,
		"EVIDENCE_OUTPUT=$RUNNER_TEMP/mds-target-evidence/run-$GITHUB_RUN_ID-attempt-$GITHUB_RUN_ATTEMPT",
		"scripts/certification-clock.sh verify",
		"target-evidence-${{ steps.certification-identity.outputs.target_kind }}-${{ github.sha }}-${{ steps.certification-identity.outputs.cohort }}-${{ github.run_id }}-${{ github.run_attempt }}",
		"wsl-guest:Ubuntu-26.04",
		"lima-guest:mds",
		"mds-macos-host",
		"mds-windows-host",
		"mds-wsl-guest",
		"mds-lima-guest",
		`test "$RUNNER_LABEL" = "$expected_runner_label"`,
		"macos-host:local)\n              target_kind=macos-host\n              expected_runner_label=mds-macos-host\n              certification_profile=certification-macos-host",
		"windows-host:local)\n              target_kind=windows-host\n              expected_runner_label=mds-windows-host\n              certification_profile=certification-windows-host",
		"wsl-guest:Ubuntu-26.04)\n              target_kind=wsl-guest\n              expected_runner_label=mds-wsl-guest\n              certification_profile=certification-wsl-guest",
		"lima-guest:mds)\n              target_kind=lima-guest\n              expected_runner_label=mds-lima-guest\n              certification_profile=certification-lima-guest",
		`echo "certification_profile=$certification_profile"`,
		`echo "cohort=$CERTIFICATION_COHORT"`,
		"CERTIFICATION_PROFILE: ${{ steps.certification-identity.outputs.certification_profile }}",
		"scripts/install-gitleaks.sh",
		"scripts/install-gitleaks.ps1",
		"Install pinned Gitleaks on Windows",
		"if: runner.os == 'Windows'",
		"shell: powershell",
		`mds_gitleaks="$RUNNER_TEMP/mds-tools/gitleaks.exe"`,
		`grep -R -q -E '"(image_)?creation_nonce"[[:space:]]*:'`,
		`grep -R -F -q -f - -- "$EVIDENCE_OUTPUT"`,
		"verified evidence contains raw guest creation nonce bytes",
		"unsupported promotion target ID: $TARGET_ID",
		"Exact reviewed certification profile plan digest for this target",
		"      - name: Upload verified certification output\n        if: success()\n        uses: actions/upload-artifact@",
		"if-no-files-found: error",
		"timeout-minutes: 240",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("target certification workflow missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"expected_commit:",
		"inputs.expected_commit",
		"expected_guest_creation_nonce:",
		"inputs.expected_guest_creation_nonce",
		"--expected-guest-creation-nonce",
		"inputs.certification_profile",
		"--all",
		"--require-publication-acceptable",
		"${{ secrets.",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("target certification workflow contains %q", forbidden)
		}
	}
}

func TestRunnerRegistrationKeepsRequiredSelfHostedLabel(t *testing.T) {
	root := repositoryRoot(t)
	content, err := os.ReadFile(
		filepath.Join(root, "docs", "operations", "target-certification-runner.md"),
	)
	if err != nil {
		t.Fatalf("read target runner runbook: %v", err)
	}
	runbook := string(content)
	if !strings.Contains(runbook, "기본 `self-hosted` label") {
		t.Fatal("runner runbook does not preserve the required self-hosted label")
	}
	if strings.Contains(runbook, "`--no-default-labels`, 표의") {
		t.Fatal("runner runbook still instructs users to remove default labels")
	}
	for _, required := range []string{
		"Git for Windows",
		"`git.exe`",
		"`bash.exe`",
		"Windows PowerShell 5.1",
		"bash -lc 'git --version && grep --version && curl --version'",
	} {
		if !strings.Contains(runbook, required) {
			t.Fatalf("runner runbook missing Windows preflight %q", required)
		}
	}
}

func TestPullRequestCIRunsWindowsBuildAndTests(t *testing.T) {
	root := repositoryRoot(t)
	content, err := os.ReadFile(
		filepath.Join(root, ".github", "workflows", "ci.yml"),
	)
	if err != nil {
		t.Fatalf("read CI workflow: %v", err)
	}
	workflow := string(content)
	for _, required := range []string{
		"windows-verify:",
		"runs-on: windows-latest",
		"persist-credentials: false",
		"go test ./...",
		"go build ./cmd/mds",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("CI workflow missing Windows contract %q", required)
		}
	}
}

func TestWorkflowsPinReviewedActionCommits(t *testing.T) {
	root := repositoryRoot(t)
	expected := []string{
		"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1",
		"actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e",
		"actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02",
		"actions/download-artifact@d3f86a106a0bac45b974a628896c90dbdf5c8093",
	}
	for _, name := range []string{"ci.yml", "release.yml", "target-certification.yml"} {
		content, err := os.ReadFile(
			filepath.Join(root, ".github", "workflows", name),
		)
		if err != nil {
			t.Fatalf("read workflow %s: %v", name, err)
		}
		workflow := string(content)
		for _, moving := range []string{"@v1", "@v2", "@v3", "@v4", "@v5", "@v6", "@main", "@master"} {
			if strings.Contains(workflow, moving) {
				t.Fatalf("workflow %s contains moving action reference %q", name, moving)
			}
		}
		for _, action := range expected {
			actionName, _, _ := strings.Cut(action, "@")
			if strings.Contains(workflow, actionName+"@") &&
				!strings.Contains(workflow, action) {
				t.Fatalf("workflow %s does not pin %s to reviewed commit", name, actionName)
			}
		}
	}
}

func TestBootstrapDownloadersKeepRedirectTimeoutAndBodyBounds(t *testing.T) {
	root := repositoryRoot(t)
	for name, required := range map[string][]string{
		"bootstrap/macos.sh": {
			"--location --max-redirs 3",
			"--proto '=https' --proto-redir '=https'",
			"--connect-timeout 30 --max-time 600 --max-filesize 536870912",
			"ulimit -f 1048576",
		},
		"internal/adapters/host/guest-bootstrap.sh": {
			"--location --max-redirs 3",
			"--proto '=https' --proto-redir '=https'",
			"--connect-timeout 30 --max-time 600 --max-filesize 268435456",
			"ulimit -f 524288",
			"stdin)",
			`cat > "$archive"`,
		},
		"bootstrap/windows.ps1": {
			"$Handler.AllowAutoRedirect = $false",
			"$redirectCount -le 3",
			"CancellationTokenSource",
			"$cancellation.CancelAfter($Timeout)",
			"ResponseHeadersRead",
			"ReadAsync(",
			"$cancellation.Token",
			"[long]$MaximumBytes = 536870912",
			"$total -gt $MaximumBytes",
		},
	} {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		script := string(content)
		for _, contract := range required {
			if !strings.Contains(script, contract) {
				t.Fatalf("%s missing bounded download contract %q", name, contract)
			}
		}
	}
}

func TestEvidenceDiscoveryBindsExactRunIdentityAndTargetCardinality(
	t *testing.T,
) {
	root := repositoryRoot(t)
	content, err := os.ReadFile(
		filepath.Join(root, "scripts", "download-target-evidence.sh"),
	)
	if err != nil {
		t.Fatalf("read evidence discovery script: %v", err)
	}
	script := string(content)
	for _, required := range []string{
		"actions/workflows/target-certification.yml/runs",
		`.head_sha == $commit`,
		`.conclusion == "success"`,
		`(.created_at | fromdateiso8601) <= ($cohort_start + 14400)`,
		`if ((mds_run_count > 16)); then`,
		`.size_in_bytes`,
		"33554432",
		"ulimit -f 65536",
		"mds-release extract-evidence",
		"MDS_CERTIFICATION_COHORT",
		`+ $commit + "-" + $cohort + "-" + $run_id + "-" + $run_attempt + "$"`,
		"for mds_kind in macos-host windows-host wsl-guest lima-guest",
		"expected exactly one $mds_kind artifact",
		`"$MDS_EVIDENCE_ROOT/$mds_name"`,
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("evidence discovery script missing %q", required)
		}
	}
}

func TestEvidenceDiscoveryExecutesExactBoundedArtifactSelection(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is required for the evidence discovery contract")
	}
	for _, tool := range []string{"go", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is required for the evidence discovery contract")
		}
	}

	t.Run("extracts one exact full-identity bundle per target", func(t *testing.T) {
		result := runEvidenceDiscoveryContract(t, false, false, false)
		if result.err != nil {
			t.Fatalf("discovery failed: %v\n%s", result.err, result.output)
		}
		for index, kind := range []string{
			"macos-host",
			"windows-host",
			"wsl-guest",
			"lima-guest",
		} {
			runID := 101 + index
			name := "target-evidence-" + kind + "-" +
				strings.Repeat("a", 40) + "-" +
				"cert-20260731T120000Z-aaaaaaaa-" +
				strconv.Itoa(runID) + "-1"
			for _, file := range []string{
				"checksums.txt",
				"doctor.json",
				"manifest.json",
				"plan.json",
			} {
				if _, err := os.Stat(
					filepath.Join(result.evidenceRoot, name, file),
				); err != nil {
					t.Fatalf("missing extracted %s/%s: %v", name, file, err)
				}
			}
		}
		if strings.Contains(result.log, "actions/runs/99/artifacts") {
			t.Fatal("discovery queried an old run outside the cohort window")
		}
	})

	t.Run("duplicate exact target artifact fails", func(t *testing.T) {
		result := runEvidenceDiscoveryContract(t, true, false, false)
		if result.err == nil ||
			!strings.Contains(result.output, "expected exactly one macos-host") {
			t.Fatalf(
				"duplicate discovery error=%v output=%q",
				result.err,
				result.output,
			)
		}
	})

	t.Run("extra zip entry fails", func(t *testing.T) {
		result := runEvidenceDiscoveryContract(t, false, true, false)
		if result.err == nil ||
			!strings.Contains(
				result.output,
				"contains 5 entries, want 4",
			) {
			t.Fatalf(
				"extra-entry discovery error=%v output=%q",
				result.err,
				result.output,
			)
		}
	})

	t.Run("oversized artifact metadata fails before download", func(t *testing.T) {
		result := runEvidenceDiscoveryContract(t, false, false, true)
		if result.err == nil ||
			!strings.Contains(result.output, "exceeds the 32 MiB archive limit") {
			t.Fatalf(
				"oversized discovery error=%v output=%q",
				result.err,
				result.output,
			)
		}
		if strings.Contains(result.log, "actions/artifacts/201/zip") {
			t.Fatal("oversized evidence artifact was downloaded")
		}
	})
}

type evidenceDiscoveryContractResult struct {
	output       string
	log          string
	evidenceRoot string
	err          error
}

func runEvidenceDiscoveryContract(
	t *testing.T,
	duplicate,
	extraEntry,
	oversizedMetadata bool,
) evidenceDiscoveryContractResult {
	t.Helper()
	root := repositoryRoot(t)
	fixture := t.TempDir()
	bin := filepath.Join(fixture, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(fixture, "evidence.zip")
	writeEvidenceContractZip(t, zipPath, extraEntry)
	logPath := filepath.Join(fixture, "gh.log")
	evidenceRoot := filepath.Join(fixture, "evidence")
	writeExecutable(t, filepath.Join(bin, "gh"), `#!/usr/bin/env bash
set -euo pipefail
endpoint=
for argument in "$@"; do
  endpoint=$argument
done
printf '%s\n' "$endpoint" >> "$MDS_FAKE_GH_LOG"
case "$endpoint" in
  *actions/workflows/target-certification.yml/runs*)
    printf '{"workflow_runs":['
    printf '{"id":99,"head_sha":"%s","event":"workflow_dispatch","conclusion":"success","run_attempt":1,"created_at":"2026-07-30T00:00:00Z"},' "$MDS_COMMIT"
    printf '{"id":101,"head_sha":"%s","event":"workflow_dispatch","conclusion":"success","run_attempt":1,"created_at":"2026-07-31T12:01:00Z"},' "$MDS_COMMIT"
    printf '{"id":102,"head_sha":"%s","event":"workflow_dispatch","conclusion":"success","run_attempt":1,"created_at":"2026-07-31T12:02:00Z"},' "$MDS_COMMIT"
    printf '{"id":103,"head_sha":"%s","event":"workflow_dispatch","conclusion":"success","run_attempt":1,"created_at":"2026-07-31T12:03:00Z"},' "$MDS_COMMIT"
    printf '{"id":104,"head_sha":"%s","event":"workflow_dispatch","conclusion":"success","run_attempt":1,"created_at":"2026-07-31T12:04:00Z"}]}\n' "$MDS_COMMIT"
    ;;
  *actions/runs/*/artifacts*)
    case "$endpoint" in
      *actions/runs/101/*) kind=macos-host; artifact_id=201; run_id=101 ;;
      *actions/runs/102/*) kind=windows-host; artifact_id=202; run_id=102 ;;
      *actions/runs/103/*) kind=wsl-guest; artifact_id=203; run_id=103 ;;
      *actions/runs/104/*) kind=lima-guest; artifact_id=204; run_id=104 ;;
      *) exit 9 ;;
    esac
    exact="target-evidence-$kind-$MDS_COMMIT-$MDS_CERTIFICATION_COHORT-$run_id-1"
	    printf '{"artifacts":[{"name":"%s","id":%s,"expired":false,"size_in_bytes":%s}' "$exact" "$artifact_id" "$MDS_FAKE_ARTIFACT_SIZE"
	    if [ "$MDS_FAKE_DUPLICATE" = 1 ] && [ "$kind" = macos-host ]; then
	      printf ',{"name":"%s","id":299,"expired":false,"size_in_bytes":%s}' "$exact" "$MDS_FAKE_ARTIFACT_SIZE"
	    fi
	    printf ',{"name":"target-evidence-%s-%s-cert-20260731T130000Z-%s-%s-1","id":300,"expired":false,"size_in_bytes":%s}' "$kind" "$MDS_COMMIT" "${MDS_COMMIT:0:8}" "$run_id" "$MDS_FAKE_ARTIFACT_SIZE"
	    printf ',{"name":"%s","id":301,"expired":true,"size_in_bytes":%s}]}\n' "$exact" "$MDS_FAKE_ARTIFACT_SIZE"
    ;;
  *actions/artifacts/*/zip)
    cat "$MDS_FAKE_ZIP"
    ;;
  *)
    exit 8
    ;;
esac
`)
	duplicateValue := "0"
	if duplicate {
		duplicateValue = "1"
	}
	archiveInfo, err := os.Stat(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	artifactSize := strconv.FormatInt(archiveInfo.Size(), 10)
	if oversizedMetadata {
		artifactSize = "33554433"
	}
	commit := strings.Repeat("a", 40)
	command := bashFixtureCommand(
		root,
		bin,
		filepath.Join(root, "scripts", "download-target-evidence.sh"),
	)
	command.Env = environmentWith(map[string]string{
		"GH_TOKEN":                 "fixture-token",
		"GITHUB_REPOSITORY":        "example/my-desk-setup",
		"MDS_COMMIT":               commit,
		"MDS_CERTIFICATION_COHORT": "cert-20260731T120000Z-aaaaaaaa",
		"MDS_EVIDENCE_ROOT":        evidenceRoot,
		"MDS_FAKE_GH_LOG":          logPath,
		"MDS_FAKE_ZIP":             zipPath,
		"MDS_FAKE_DUPLICATE":       duplicateValue,
		"MDS_FAKE_ARTIFACT_SIZE":   artifactSize,
	})
	output, err := command.CombinedOutput()
	log, readErr := os.ReadFile(logPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	return evidenceDiscoveryContractResult{
		output:       string(output),
		log:          string(log),
		evidenceRoot: evidenceRoot,
		err:          err,
	}
}

func writeEvidenceContractZip(t *testing.T, path string, extra bool) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	names := []string{
		"checksums.txt",
		"doctor.json",
		"manifest.json",
		"plan.json",
	}
	if extra {
		names = append(names, "unexpected.txt")
	}
	for _, name := range names {
		entry, createErr := writer.Create(name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := entry.Write([]byte("fixture-" + name + "\n")); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReleasePublicationIsDraftFirstAndIdempotent(t *testing.T) {
	root := repositoryRoot(t)
	content, err := os.ReadFile(
		filepath.Join(root, "scripts", "publish-release.sh"),
	)
	if err != nil {
		t.Fatalf("read release publication script: %v", err)
	}
	script := string(content)
	for _, required := range []string{
		`git rev-list -n 1 "$MDS_RELEASE_TAG"`,
		`gh release create "$MDS_RELEASE_TAG"`,
		"--draft",
		`gh release upload "$MDS_RELEASE_TAG"`,
		`"$MDS_EVIDENCE_ARCHIVE_DIR"/*`,
		`go run ./cmd/mds-release verify-promotion`,
		`"$mds_verified_evidence"/*`,
		"--clobber",
		`gh release download "$MDS_RELEASE_TAG"`,
		`cmp -s "$mds_local"`,
		`gh release edit "$MDS_RELEASE_TAG"`,
		"--draft=false",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("release publication script missing %q", required)
		}
	}
}

func TestReleasePublicationFailsClosedAndPublishesOnlyVerifiedBytes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the publication contract executes its POSIX shell script")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is required for the publication contract")
	}

	t.Run("lookup failure does not create release", func(t *testing.T) {
		result := runPublishReleaseContract(t, "500", false)
		if result.err == nil {
			t.Fatal("publish succeeded after GitHub lookup failure")
		}
		if result.log != "verify\nverify-promotion\napi\n" {
			t.Fatalf(
				"GitHub calls = %q, want lookup only\n%s",
				result.log,
				result.output,
			)
		}
	})

	t.Run("missing release follows exact draft-first order", func(t *testing.T) {
		result := runPublishReleaseContract(t, "404", false)
		if result.err != nil {
			t.Fatalf("publish failed: %v\n%s", result.err, result.output)
		}
		want := "verify\nverify-promotion\napi\nrelease create\nrelease upload\nrelease download\nrelease edit\n"
		if result.log != want {
			t.Fatalf("GitHub calls = %q, want %q", result.log, want)
		}
	})

	t.Run("published release is verified without mutation", func(t *testing.T) {
		result := runPublishReleaseContract(t, "200", false)
		if result.err != nil {
			t.Fatalf("published release verification failed: %v\n%s", result.err, result.output)
		}
		want := "verify\nverify-promotion\napi\nrelease download\n"
		if result.log != want {
			t.Fatalf("GitHub calls = %q, want %q", result.log, want)
		}
	})

	t.Run("byte mismatch never publishes draft", func(t *testing.T) {
		result := runPublishReleaseContract(t, "404", true)
		if result.err == nil {
			t.Fatal("publish succeeded with mismatched downloaded bytes")
		}
		want := "verify\nverify-promotion\napi\nrelease create\nrelease upload\nrelease download\n"
		if result.log != want {
			t.Fatalf("GitHub calls = %q, want no release edit", result.log)
		}
	})
}

type publishReleaseContractResult struct {
	output string
	log    string
	err    error
}

func runPublishReleaseContract(
	t *testing.T,
	apiStatus string,
	corruptDownload bool,
) publishReleaseContractResult {
	t.Helper()
	root := repositoryRoot(t)
	fixture := t.TempDir()
	bin := filepath.Join(fixture, "bin")
	releaseDir := filepath.Join(fixture, "release")
	evidenceDir := filepath.Join(fixture, "evidence")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatalf("MkdirAll(bin): %v", err)
	}
	if err := os.MkdirAll(releaseDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(release): %v", err)
	}
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(evidence): %v", err)
	}
	assetName := "mds_0.1.0_linux_amd64.tar.gz"
	if err := os.WriteFile(
		filepath.Join(releaseDir, assetName),
		[]byte("verified archive bytes"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile(asset): %v", err)
	}
	report := filepath.Join(fixture, "release-promotion.json")
	if err := os.WriteFile(report, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(report): %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(evidenceDir, "mds_0.1.0_certification_macos-host_fixture.zip"),
		[]byte("verified evidence bytes"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile(evidence): %v", err)
	}
	logPath := filepath.Join(fixture, "gh.log")
	commit := strings.Repeat("a", 40)
	writeExecutable(t, filepath.Join(bin, "git"), `#!/bin/sh
printf '%s\n' "$MDS_COMMIT"
`)
	writeExecutable(t, filepath.Join(bin, "go"), `#!/bin/sh
case " $* " in
  *" verify-promotion "*) printf 'verify-promotion\n' >> "$MDS_FAKE_GH_LOG" ;;
  *" verify "*) printf 'verify\n' >> "$MDS_FAKE_GH_LOG" ;;
  *) exit 2 ;;
esac
`)
	writeExecutable(t, filepath.Join(bin, "gh"), `#!/bin/sh
set -eu
if [ "$1" = api ]; then
  printf 'api\n' >> "$MDS_FAKE_GH_LOG"
  case "$MDS_FAKE_API_STATUS" in
    200)
      printf 'HTTP/2.0 200 OK\n\n%s\tfalse\n' "$MDS_RELEASE_TAG"
      exit 0
      ;;
    404)
      printf 'HTTP/2.0 404 Not Found\n\n'
      exit 1
      ;;
    *)
      printf 'HTTP/2.0 %s Failure\n\n' "$MDS_FAKE_API_STATUS"
      exit 1
      ;;
  esac
fi
if [ "$1" != release ]; then
  exit 2
fi
printf 'release %s\n' "$2" >> "$MDS_FAKE_GH_LOG"
if [ "$2" = download ]; then
  destination=
  previous=
  for argument in "$@"; do
    if [ "$previous" = --dir ]; then
      destination=$argument
    fi
    previous=$argument
  done
  test -n "$destination"
  cp "$MDS_FAKE_RELEASE_DIR"/* "$destination/"
  cp "$MDS_FAKE_PROMOTION_REPORT" "$destination/"
  cp "$MDS_FAKE_EVIDENCE_DIR"/* "$destination/"
  if [ "$MDS_FAKE_CORRUPT" = 1 ]; then
    printf 'different bytes\n' > "$destination/mds_0.1.0_linux_amd64.tar.gz"
  fi
fi
`)

	corrupt := "0"
	if corruptDownload {
		corrupt = "1"
	}
	command := exec.Command(
		"bash",
		filepath.Join(root, "scripts", "publish-release.sh"),
	)
	command.Dir = root
	environment := make([]string, 0, len(os.Environ())+12)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "PATH", "GH_TOKEN", "GITHUB_REPOSITORY", "MDS_RELEASE_TAG",
			"MDS_RELEASE_DIR", "MDS_PROMOTION_REPORT",
			"MDS_EVIDENCE_ARCHIVE_DIR", "MDS_COMMIT",
			"MDS_CERTIFICATION_COHORT",
			"MDS_FAKE_GH_LOG", "MDS_FAKE_API_STATUS",
			"MDS_FAKE_RELEASE_DIR", "MDS_FAKE_PROMOTION_REPORT",
			"MDS_FAKE_EVIDENCE_DIR", "MDS_FAKE_CORRUPT":
			continue
		}
		environment = append(environment, entry)
	}
	command.Env = append(
		environment,
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_TOKEN=fixture-token",
		"GITHUB_REPOSITORY=example/my-desk-setup",
		"MDS_RELEASE_TAG=v0.1.0",
		"MDS_RELEASE_DIR="+releaseDir,
		"MDS_PROMOTION_REPORT="+report,
		"MDS_EVIDENCE_ARCHIVE_DIR="+evidenceDir,
		"MDS_COMMIT="+commit,
		"MDS_CERTIFICATION_COHORT=cert-20260731T120000Z-aaaaaaaa",
		"MDS_FAKE_GH_LOG="+logPath,
		"MDS_FAKE_API_STATUS="+apiStatus,
		"MDS_FAKE_RELEASE_DIR="+releaseDir,
		"MDS_FAKE_PROMOTION_REPORT="+report,
		"MDS_FAKE_EVIDENCE_DIR="+evidenceDir,
		"MDS_FAKE_CORRUPT="+corrupt,
	)
	output, err := command.CombinedOutput()
	log, readErr := os.ReadFile(logPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("ReadFile(log): %v", readErr)
	}
	return publishReleaseContractResult{
		output: string(output),
		log:    string(log),
		err:    err,
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func bashFixtureCommand(
	root,
	fixtureBin,
	script string,
	arguments ...string,
) *exec.Cmd {
	const wrapper = `
fixture_bin=$1
script=$2
shift 2
if command -v cygpath >/dev/null 2>&1; then
  fixture_bin=$(cygpath -u "$fixture_bin")
  script=$(cygpath -u "$script")
fi
PATH="$fixture_bin:$PATH"
export PATH
exec bash "$script" "$@"
`
	commandArguments := []string{"-c", wrapper, "mds-contract", fixtureBin, script}
	commandArguments = append(commandArguments, arguments...)
	command := exec.Command("bash", commandArguments...)
	command.Dir = root
	return command
}

func environmentWith(overrides map[string]string) []string {
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[key]; replaced {
			continue
		}
		environment = append(environment, entry)
	}
	for key, value := range overrides {
		environment = append(environment, key+"="+value)
	}
	return environment
}
