package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
)

func TestApplyWiresExplicitNvChadAdoptionIntoGuestAdapter(t *testing.T) {
	home := t.TempDir()
	imageDigest := strings.Repeat("a", 64)
	creationNonce := strings.Repeat("b", 64)
	commitment, err := target.GuestCreationNonceCommitment(creationNonce)
	if err != nil {
		t.Fatalf("GuestCreationNonceCommitment(): %v", err)
	}
	var captured adapterOptions
	factoryCalls := 0
	system := Runtime{
		GOOS: "linux", GOARCH: "amd64",
		Getenv: func(key string) string {
			switch key {
			case "WSL_DISTRO_NAME":
				return "Ubuntu-26.04"
			case "MDS_IMAGE_REVISION":
				return "sha256:" + imageDigest
			case "MDS_IMAGE_PROVENANCE":
				return "https://example.invalid/ubuntu.wsl"
			case "MDS_IMAGE_CREATION_NONCE_COMMITMENT":
				return commitment
			default:
				return ""
			}
		},
		HomeDir: func() (string, error) { return home, nil },
		ObserveTarget: func(_ context.Context, facts target.Facts) (target.Facts, error) {
			facts.OS = "linux"
			facts.OSVersion = "26.04"
			facts.SystemdSupported = true
			facts.SystemdActive = true
			facts.Reachable = true
			return facts, nil
		},
		newAdapter: func(
			_ catalog.Environment,
			_ target.Facts,
			_ string,
			_ Runtime,
			options adapterOptions,
		) (adapters.Component, error) {
			factoryCalls++
			captured = options
			return alwaysReadyComponent{}, nil
		},
	}

	var planOutput bytes.Buffer
	var planError bytes.Buffer
	if code := Run(
		[]string{
			"plan", "--target", "wsl-guest:Ubuntu-26.04",
			"--component", "nvchad", "--format", "json",
		},
		Streams{Input: strings.NewReader(""), Output: &planOutput, Error: &planError},
		system,
	); code != ExitSuccess {
		t.Fatalf("plan code=%d stderr=%q", code, planError.String())
	}
	var plan planning.Plan
	if err := json.Unmarshal(planOutput.Bytes(), &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}

	var applyOutput bytes.Buffer
	var applyError bytes.Buffer
	if code := Run(
		[]string{
			"apply", "--target", "wsl-guest:Ubuntu-26.04",
			"--component", "nvchad", "--plan-digest", plan.Digest,
			"--adopt-nvchad", "--state-root", t.TempDir(), "--format", "json",
		},
		Streams{Input: strings.NewReader(""), Output: &applyOutput, Error: &applyError},
		system,
	); code != ExitSuccess {
		t.Fatalf("apply code=%d stderr=%q", code, applyError.String())
	}
	if factoryCalls != 1 {
		t.Fatalf("adapter factory calls=%d, want 1", factoryCalls)
	}
	if !captured.AllowAdopt {
		t.Fatal("--adopt-nvchad did not reach guest adapter options")
	}
}

type alwaysReadyComponent struct{}

func (alwaysReadyComponent) Observe(
	context.Context,
	planning.Action,
) (adapters.Observation, error) {
	return adapters.Observation{State: adapters.StateReady}, nil
}

func (alwaysReadyComponent) Apply(context.Context, planning.Action) error { return nil }

func (alwaysReadyComponent) Verify(context.Context, planning.Action) error { return nil }
