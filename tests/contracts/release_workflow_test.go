package contracts_test

import (
	"os"
	"path/filepath"
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
		"--expected-binary-sha256",
		"MDS_EXPECTED_GUEST_CREATION_NONCE",
		`[[ ! "${MDS_EXPECTED_GUEST_CREATION_NONCE:-}" =~ ^[0-9a-f]{64}$ ]]`,
		"--expected-guest-creation-nonce",
		"--require-publication-acceptable",
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
		"target-evidence-${{ steps.certification-identity.outputs.target_kind }}-${{ github.sha }}-${{ github.run_id }}-${{ github.run_attempt }}",
		"wsl-guest:Ubuntu-26.04",
		"lima-guest:mds",
		"mds-macos-host",
		"mds-windows-host",
		"mds-wsl-guest",
		"mds-lima-guest",
		`test "$RUNNER_LABEL" = "$expected_runner_label"`,
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
		"${{ secrets.",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("target certification workflow contains %q", forbidden)
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
		"actions/checkout@11d5960a326750d5838078e36cf38b85af677262",
		"actions/setup-go@40f1582b2485089dde7abd97c1529aa768e1baff",
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
			"--connect-timeout 30 --max-time 600 --max-filesize 536870912",
			"ulimit -f 1048576",
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
		`+ $commit + "-" + $run_id + "-" + $run_attempt + "$"`,
		"for mds_kind in macos-host windows-host wsl-guest lima-guest",
		"expected exactly one $mds_kind artifact",
		"does not contain the exact four-file bundle",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("evidence discovery script missing %q", required)
		}
	}
}
