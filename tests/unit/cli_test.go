package unit_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/cli"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/state"
)

func TestCLIPlanJSONUsesEmbeddedCatalog(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := cli.Run(
		[]string{
			"plan",
			"--target", "lima-guest:mds",
			"--component", "notion-cli",
			"--format", "json",
		},
		cli.Streams{
			Input:  strings.NewReader(""),
			Output: &stdout,
			Error:  &stderr,
		},
		cli.Runtime{
			GOOS: "darwin", GOARCH: "arm64", Getenv: func(string) string { return "" },
		},
	)
	if code != 0 {
		t.Fatalf("Run() code = %d, stderr=%q", code, stderr.String())
	}
	var plan planning.Plan
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatalf("decode plan JSON: %v\n%s", err, stdout.String())
	}
	if plan.SchemaVersion != planning.PlanSchema || plan.Digest == "" {
		t.Fatalf("plan = %+v, want schema and digest", plan)
	}
	golden, err := os.ReadFile(
		filepath.Join(repositoryRoot(t), "tests", "golden", "plans", "notion-cli-lima.json"),
	)
	if err != nil {
		t.Fatalf("read golden plan: %v", err)
	}
	normalizedGolden := bytes.TrimSpace(golden)
	normalizedActual := bytes.TrimSpace(stdout.Bytes())
	if !bytes.Equal(normalizedActual, normalizedGolden) {
		t.Fatalf("plan JSON does not match golden\nactual:\n%s", stdout.String())
	}
	for _, action := range plan.Actions {
		if action.ComponentID == "notion-desktop" {
			t.Fatalf("notion CLI plan contains desktop action")
		}
	}
}

func TestCLIRequiresExactlyOneSelectionSource(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := cli.Run(
		[]string{
			"plan",
			"--target", "macos-host:local",
			"--all",
			"--component", "wezterm",
		},
		cli.Streams{
			Input:  strings.NewReader(""),
			Output: &stdout,
			Error:  &stderr,
		},
		cli.Runtime{
			GOOS: "darwin", GOARCH: "arm64", Getenv: func(string) string { return "" },
		},
	)
	if code == 0 || !strings.Contains(stderr.String(), "choose exactly one") {
		t.Fatalf("Run() code=%d stderr=%q, want selection error", code, stderr.String())
	}
}

func TestCLIApplyRejectsStaleDigestBeforeStateMutation(t *testing.T) {
	home := t.TempDir()
	stateRoot := filepath.Join(home, "state")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := cli.Run(
		[]string{
			"apply",
			"--target", "macos-host:local",
			"--component", "xcode",
			"--plan-digest", "sha256:not-reviewed",
			"--state-root", stateRoot,
			"--format", "json",
		},
		cli.Streams{
			Input: strings.NewReader(""), Output: &stdout, Error: &stderr,
		},
		cli.Runtime{
			GOOS: "darwin", GOARCH: "arm64",
			Getenv:  func(string) string { return "" },
			HomeDir: func() (string, error) { return home, nil },
		},
	)
	if code == 0 || !strings.Contains(stderr.String(), "digest mismatch") {
		t.Fatalf("Run() code=%d stderr=%q, want digest mismatch", code, stderr.String())
	}
	if _, err := os.Stat(stateRoot); !os.IsNotExist(err) {
		t.Fatalf("state root exists after stale digest: %v", err)
	}
}

func TestCLIApplyEmitsActionRequiredWithoutAuth(t *testing.T) {
	home := t.TempDir()
	system := cli.Runtime{
		GOOS: "darwin", GOARCH: "arm64",
		Getenv:  func(string) string { return "" },
		HomeDir: func() (string, error) { return home, nil },
	}
	var planOutput bytes.Buffer
	var planError bytes.Buffer
	if code := cli.Run(
		[]string{
			"plan",
			"--target", "macos-host:local",
			"--component", "xcode",
			"--format", "json",
		},
		cli.Streams{
			Input: strings.NewReader(""), Output: &planOutput, Error: &planError,
		},
		system,
	); code != 0 {
		t.Fatalf("plan code=%d stderr=%q", code, planError.String())
	}
	var plan planning.Plan
	if err := json.Unmarshal(planOutput.Bytes(), &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}

	var applyOutput bytes.Buffer
	var applyError bytes.Buffer
	code := cli.Run(
		[]string{
			"apply",
			"--target", "macos-host:local",
			"--component", "xcode",
			"--plan-digest", plan.Digest,
			"--state-root", filepath.Join(home, "state"),
			"--format", "json",
		},
		cli.Streams{
			Input: strings.NewReader(""), Output: &applyOutput, Error: &applyError,
		},
		system,
	)
	if code == 0 || !strings.Contains(applyError.String(), "unresolved") {
		t.Fatalf("apply code=%d stderr=%q, want unresolved outcome", code, applyError.String())
	}
	var receipt state.Receipt
	if err := json.Unmarshal(applyOutput.Bytes(), &receipt); err != nil {
		t.Fatalf("decode receipt: %v\n%s", err, applyOutput.String())
	}
	if receipt.Complete || len(receipt.Outcomes) != 1 ||
		receipt.Outcomes[0].Status != "action-required" {
		t.Fatalf("receipt = %+v, want action-required", receipt)
	}
	combined := strings.ToLower(planOutput.String() + applyOutput.String())
	for _, forbidden := range []string{" auth login", " login ", "token", "credential"} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("plan/apply output contains auth surface %q", forbidden)
		}
	}
}
