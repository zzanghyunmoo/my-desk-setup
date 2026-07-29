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
