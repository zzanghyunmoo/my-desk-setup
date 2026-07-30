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
		"expected_commit:",
		"expected_binary_sha256:",
		"--expected-binary-sha256",
		"--require-publication-acceptable",
		"continue-on-error: true",
		"target-evidence-${{ steps.certification-identity.outputs.target_kind }}-${{ inputs.expected_commit }}-${{ github.run_id }}-${{ github.run_attempt }}",
		"ref: ${{ inputs.expected_commit }}",
		`test "$(git rev-parse HEAD)" = "$EXPECTED_COMMIT"`,
		"wsl-guest:Ubuntu-26.04",
		"lima-guest:mds",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("target certification workflow missing %q", required)
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
