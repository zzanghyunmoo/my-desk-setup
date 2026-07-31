package contracts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

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
		`--cohort "$CERTIFICATION_COHORT"`,
		`--expected-cohort "$CERTIFICATION_COHORT"`,
		"MDS_EXPECTED_GUEST_CREATION_NONCE",
		`[[ ! "${MDS_EXPECTED_GUEST_CREATION_NONCE:-}" =~ ^[0-9a-f]{64}$ ]]`,
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
		`if [[ "$RUNNER_OS" == Windows ]]`,
		`mds_gitleaks="$RUNNER_TEMP/mds-tools/gitleaks.exe"`,
		`grep -R -q -E '"(image_)?creation_nonce"[[:space:]]*:'`,
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
		"MDS_CERTIFICATION_COHORT",
		`+ $commit + "-" + $cohort + "-" + $run_id + "-" + $run_attempt + "$"`,
		"for mds_kind in macos-host windows-host wsl-guest lima-guest",
		"expected exactly one $mds_kind artifact",
		"does not contain the exact four-file bundle",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("evidence discovery script missing %q", required)
		}
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
		if result.log != "api\n" {
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
		want := "api\nrelease create\nrelease upload\nrelease download\nrelease edit\n"
		if result.log != want {
			t.Fatalf("GitHub calls = %q, want %q", result.log, want)
		}
	})

	t.Run("published release is verified without mutation", func(t *testing.T) {
		result := runPublishReleaseContract(t, "200", false)
		if result.err != nil {
			t.Fatalf("published release verification failed: %v\n%s", result.err, result.output)
		}
		want := "api\nrelease download\n"
		if result.log != want {
			t.Fatalf("GitHub calls = %q, want %q", result.log, want)
		}
	})

	t.Run("byte mismatch never publishes draft", func(t *testing.T) {
		result := runPublishReleaseContract(t, "404", true)
		if result.err == nil {
			t.Fatal("publish succeeded with mismatched downloaded bytes")
		}
		want := "api\nrelease create\nrelease upload\nrelease download\n"
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
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatalf("MkdirAll(bin): %v", err)
	}
	if err := os.MkdirAll(releaseDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(release): %v", err)
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
	logPath := filepath.Join(fixture, "gh.log")
	commit := strings.Repeat("a", 40)
	writeExecutable(t, filepath.Join(bin, "git"), `#!/bin/sh
printf '%s\n' "$MDS_COMMIT"
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
			"MDS_RELEASE_DIR", "MDS_PROMOTION_REPORT", "MDS_COMMIT",
			"MDS_FAKE_GH_LOG", "MDS_FAKE_API_STATUS",
			"MDS_FAKE_RELEASE_DIR", "MDS_FAKE_PROMOTION_REPORT",
			"MDS_FAKE_CORRUPT":
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
		"MDS_COMMIT="+commit,
		"MDS_FAKE_GH_LOG="+logPath,
		"MDS_FAKE_API_STATUS="+apiStatus,
		"MDS_FAKE_RELEASE_DIR="+releaseDir,
		"MDS_FAKE_PROMOTION_REPORT="+report,
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
