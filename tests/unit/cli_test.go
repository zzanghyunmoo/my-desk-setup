package unit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/cli"
	"github.com/zzanghyunmoo/my-desk-setup/internal/doctor"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/state"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/version"
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

func TestCLIExplicitCurrentGuestPreservesObservedImageIdentity(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var observed target.Facts
	digest := strings.Repeat("a", 64)
	code := cli.Run(
		[]string{
			"plan",
			"--target", "wsl-guest:Ubuntu-26.04",
			"--component", "notion-cli",
			"--format", "json",
		},
		cli.Streams{
			Input: strings.NewReader(""), Output: &stdout, Error: &stderr,
		},
		cli.Runtime{
			GOOS: "linux", GOARCH: "amd64",
			Getenv: func(key string) string {
				switch key {
				case "WSL_DISTRO_NAME":
					return "Ubuntu-26.04"
				case "MDS_IMAGE_REVISION":
					return "sha256:" + digest
				case "MDS_IMAGE_PROVENANCE":
					return "https://example.invalid/ubuntu.wsl"
				case "MDS_IMAGE_CREATION_NONCE":
					return strings.Repeat("b", 64)
				default:
					return ""
				}
			},
			ObserveTarget: func(
				_ context.Context,
				facts target.Facts,
			) (target.Facts, error) {
				observed = facts
				facts.OSVersion = "26.04"
				facts.RuntimeVersion = "fixture"
				facts.SystemdSupported = true
				facts.SystemdActive = true
				return facts, nil
			},
		},
	)
	if code != 0 {
		t.Fatalf("Run() code = %d stderr=%q", code, stderr.String())
	}
	if observed.ImageRevision != "sha256:"+digest ||
		observed.ImageProvenance != "https://example.invalid/ubuntu.wsl" ||
		observed.ImageCreationNonce != strings.Repeat("b", 64) {
		t.Fatalf("ObserveTarget input lost guest image identity: %+v", observed)
	}
}

func TestCLIVersionCommandRemainsBackwardCompatible(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := cli.Run(
		[]string{"version"},
		cli.Streams{
			Input: strings.NewReader(""), Output: &stdout, Error: &stderr,
		},
		cli.Runtime{},
	)
	if code != cli.ExitSuccess || strings.TrimSpace(stdout.String()) == "" {
		t.Fatalf(
			"version code=%d stdout=%q stderr=%q",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
}

func TestCLIVersionJSONIsOneClosedMachineReadableDocument(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := cli.Run(
		[]string{"version", "--format", "json"},
		cli.Streams{
			Input: strings.NewReader(""), Output: &stdout, Error: &stderr,
		},
		cli.Runtime{},
	)
	if code != cli.ExitSuccess || stderr.Len() != 0 {
		t.Fatalf(
			"version code=%d stdout=%q stderr=%q",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
	var envelope struct {
		SchemaVersion string `json:"schema_version"`
		Version       string `json:"version"`
		Commit        string `json:"commit"`
		Date          string `json:"date"`
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		t.Fatalf("decode version JSON: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("version output contains trailing JSON: %v", err)
	}
	if envelope.SchemaVersion != cli.VersionSchema ||
		envelope.Version != version.Version ||
		envelope.Commit != version.Commit ||
		envelope.Date != version.Date {
		t.Fatalf("version envelope = %+v", envelope)
	}
}

func TestCLICatalogJSONIsMachineReadable(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := cli.Run(
		[]string{"catalog", "--format", "json"},
		cli.Streams{
			Input: strings.NewReader(""), Output: &stdout, Error: &stderr,
		},
		cli.Runtime{},
	)
	if code != cli.ExitSuccess {
		t.Fatalf("catalog code=%d stderr=%q", code, stderr.String())
	}
	var envelope struct {
		SchemaVersion string `json:"schema_version"`
		Environment   struct {
			Catalog struct {
				Components []struct {
					ID       string                    `json:"id"`
					Provides []string                  `json:"provides"`
					Targets  map[string]map[string]any `json:"targets"`
				} `json:"components"`
			} `json:"catalog"`
			Profiles map[string]any `json:"profiles"`
			Targets  map[string]any `json:"targets"`
		} `json:"environment"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode catalog JSON: %v", err)
	}
	if envelope.SchemaVersion != cli.CatalogSchema ||
		len(envelope.Environment.Catalog.Components) == 0 ||
		len(envelope.Environment.Profiles) == 0 ||
		len(envelope.Environment.Targets) == 0 {
		t.Fatalf("catalog envelope = %+v", envelope)
	}
	for _, component := range envelope.Environment.Catalog.Components {
		if component.ID == "" || len(component.Provides) == 0 ||
			len(component.Targets) != 4 {
			t.Fatalf("component is not agent-discoverable: %+v", component)
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

func TestCLIJSONErrorTaxonomy(t *testing.T) {
	tests := []struct {
		name        string
		arguments   []string
		system      cli.Runtime
		wantExit    int
		wantStatus  string
		wantCode    string
		wantMessage string
	}{
		{
			name: "invalid input",
			arguments: []string{
				"plan",
				"--target", "macos-host:local",
				"--all",
				"--component", "wezterm",
				"--format", "json",
			},
			system: cli.Runtime{
				GOOS: "darwin", GOARCH: "arm64",
				Getenv: func(string) string { return "" },
			},
			wantExit:    cli.ExitInvalidInput,
			wantStatus:  "error",
			wantCode:    "invalid-input",
			wantMessage: "command input is invalid",
		},
		{
			name:      "unknown command",
			arguments: []string{"frobnicate", "--format=json"},
			system: cli.Runtime{
				GOOS: "darwin", GOARCH: "arm64",
				Getenv: func(string) string { return "" },
			},
			wantExit:    cli.ExitInvalidInput,
			wantStatus:  "error",
			wantCode:    "invalid-input",
			wantMessage: "command input is invalid",
		},
		{
			name: "unreachable target",
			arguments: []string{
				"plan",
				"--target", "macos-host:local",
				"--all",
				"--format", "json",
			},
			system: cli.Runtime{
				GOOS: "darwin", GOARCH: "arm64",
				Getenv: func(string) string { return "" },
				ObserveTarget: func(
					context.Context,
					target.Facts,
				) (target.Facts, error) {
					return target.Facts{}, errors.New("fixture target is offline")
				},
			},
			wantExit:    cli.ExitUnreachable,
			wantStatus:  "unreachable",
			wantCode:    "unreachable",
			wantMessage: "target is unreachable",
		},
		{
			name: "internal failure",
			arguments: []string{
				"doctor",
				"--target", "macos-host:local",
				"--component", "xcode",
				"--format", "json",
			},
			system: cli.Runtime{
				GOOS: "darwin", GOARCH: "arm64",
				Getenv: func(string) string { return "" },
				HomeDir: func() (string, error) {
					return "", errors.New("fixture home lookup failed")
				},
			},
			wantExit:    cli.ExitInternal,
			wantStatus:  "error",
			wantCode:    "internal",
			wantMessage: "internal command failure",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := cli.Run(
				test.arguments,
				cli.Streams{
					Input: strings.NewReader(""), Output: &stdout, Error: &stderr,
				},
				test.system,
			)
			if code != test.wantExit {
				t.Fatalf(
					"Run() code=%d, want %d; stderr=%q",
					code,
					test.wantExit,
					stderr.String(),
				)
			}
			if stdout.Len() != 0 {
				t.Fatalf("Run() stdout=%q, want empty", stdout.String())
			}
			envelope := decodeCLIError(t, stderr.Bytes())
			if envelope.SchemaVersion != cli.ErrorSchema ||
				envelope.Status != test.wantStatus ||
				envelope.Code != test.wantCode ||
				envelope.Message != test.wantMessage ||
				envelope.RecoveryHint == "" ||
				envelope.Details.Cause == "" {
				t.Fatalf("error envelope = %+v", envelope)
			}
		})
	}
}

func TestCLIMutatingCommandsRejectInvalidFormatBeforeWork(t *testing.T) {
	for _, command := range [][]string{
		{
			"apply", "--component", "xcode",
			"--plan-digest", "sha256:not-reviewed", "--format", "yaml",
		},
		{"update", "--component", "typescript", "--format", "yaml"},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := cli.Run(
			command,
			cli.Streams{
				Input: strings.NewReader(""), Output: &stdout, Error: &stderr,
			},
			cli.Runtime{
				GOOS: "darwin", GOARCH: "arm64",
				Getenv:  func(string) string { return "" },
				HomeDir: func() (string, error) { return t.TempDir(), nil },
			},
		)
		if code == 0 || !strings.Contains(stderr.String(), "unsupported format") {
			t.Fatalf(
				"Run(%v) code=%d stderr=%q, want early format rejection",
				command,
				code,
				stderr.String(),
			)
		}
		if stdout.Len() != 0 {
			t.Fatalf("Run(%v) wrote stdout before format rejection: %q", command, stdout.String())
		}
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
	if code != cli.ExitStalePlan {
		t.Fatalf(
			"Run() code=%d stderr=%q, want stale-plan exit %d",
			code,
			stderr.String(),
			cli.ExitStalePlan,
		)
	}
	envelope := decodeCLIError(t, stderr.Bytes())
	if envelope.Code != "stale-plan" ||
		envelope.Status != "stale" ||
		envelope.Message != "reviewed plan is stale" {
		t.Fatalf("error envelope = %+v, want stale-plan", envelope)
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
	if code != cli.ExitActionRequired {
		t.Fatalf(
			"apply code=%d stderr=%q, want action-required exit %d",
			code,
			applyError.String(),
			cli.ExitActionRequired,
		)
	}
	envelope := decodeCLIError(t, applyError.Bytes())
	if envelope.Code != "action-required" ||
		envelope.Status != "action-required" ||
		envelope.Message != "command requires user action" {
		t.Fatalf("error envelope = %+v, want action-required", envelope)
	}
	var receipt state.Receipt
	decodeSingleJSON(t, applyOutput.Bytes(), &receipt)
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

func TestCLIDoctorPartialReportRemainsSingleJSONDocument(t *testing.T) {
	home := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := cli.Run(
		[]string{
			"doctor",
			"--target", "macos-host:local",
			"--component", "xcode",
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
	if code != cli.ExitActionRequired {
		t.Fatalf(
			"doctor code=%d stderr=%q, want action-required exit %d",
			code,
			stderr.String(),
			cli.ExitActionRequired,
		)
	}
	var report doctor.Report
	decodeSingleJSON(t, stdout.Bytes(), &report)
	if report.Ready || report.SchemaVersion != doctor.SchemaVersion {
		t.Fatalf("doctor report = %+v, want versioned partial report", report)
	}
	envelope := decodeCLIError(t, stderr.Bytes())
	if envelope.Code != "action-required" {
		t.Fatalf("error envelope = %+v, want action-required", envelope)
	}
}

func decodeCLIError(t *testing.T, content []byte) cli.ErrorEnvelope {
	t.Helper()
	var envelope cli.ErrorEnvelope
	decodeSingleJSON(t, content, &envelope)
	return envelope
}

func decodeSingleJSON(t *testing.T, content []byte, destination any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, content)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("JSON has trailing content: %v\n%s", err, content)
	}
}
