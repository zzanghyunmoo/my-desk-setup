package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

type UnityEditor struct {
	Platform     string
	Architecture string
	Port         transport.Port
	Executable   string
}

type unityCLIEnvelope struct {
	Success bool              `json:"success"`
	Data    []unityEditorInfo `json:"data"`
}

type unityEditorInfo struct {
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
}

func (editor UnityEditor) Observe(ctx context.Context, action planning.Action) (adapters.Observation, error) {
	if editor.Port == nil {
		return adapters.Observation{}, errors.New("Unity Editor adapter requires a port")
	}
	reviewedArchitecture, err := editor.reviewedArchitecture()
	if err != nil {
		return adapters.Observation{State: adapters.StateConflict, Detail: err.Error()}, nil
	}
	wantedChangeset := action.Inputs["install_ref"]
	if action.Version == "" || wantedChangeset == "" {
		return adapters.Observation{
			State:  adapters.StateConflict,
			Detail: "Unity Editor action requires exact version and changeset",
		}, nil
	}
	result, err := editor.Port.Run(ctx, editor.command(
		"editors", "--installed", "--format", "json",
	))
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return adapters.Observation{State: adapters.StateAbsent}, nil
		}
		return adapters.Observation{State: adapters.StateConflict, Detail: err.Error()}, nil
	}
	var envelope unityCLIEnvelope
	if err := json.Unmarshal([]byte(result.Stdout), &envelope); err != nil || !envelope.Success {
		return adapters.Observation{State: adapters.StateConflict, Detail: "Unity CLI returned invalid installed-editor inventory"}, nil
	}
	for _, installed := range envelope.Data {
		if installed.Version != action.Version {
			continue
		}
		// Unity CLI beta.8 does not expose the changeset in installed-editor
		// inventory. The reviewed changeset remains a required, digest-bound
		// install input; the installed release is identified by its unique
		// version and architecture, then structurally checked by Verify.
		if !matchesUnityArchitecture(reviewedArchitecture, installed.Architecture) {
			return adapters.Observation{State: adapters.StateConflict, InstalledVersion: installed.Version, Detail: "installed Unity Editor architecture differs from reviewed target"}, nil
		}
		return adapters.Observation{State: adapters.StateReady, InstalledVersion: installed.Version}, nil
	}
	return adapters.Observation{State: adapters.StateAbsent}, nil
}

func (editor UnityEditor) Apply(ctx context.Context, action planning.Action) error {
	observation, err := editor.Observe(ctx, action)
	if err != nil {
		return err
	}
	if observation.State == adapters.StateReady {
		return nil
	}
	if observation.State == adapters.StateConflict {
		return errors.New(observation.Detail)
	}
	changeset := action.Inputs["install_ref"]
	reviewedArchitecture, err := editor.reviewedArchitecture()
	if err != nil {
		return err
	}
	command := editor.command(
		"install", action.Version,
		"--changeset", changeset,
		"--yes",
		"--accept-eula",
		"--architecture", reviewedArchitecture,
	)
	command.Timeout = 2 * time.Hour
	_, err = editor.Port.Run(ctx, command)
	return err
}

func (editor UnityEditor) Verify(ctx context.Context, action planning.Action) error {
	observation, err := editor.Observe(ctx, action)
	if err != nil {
		return err
	}
	if observation.State != adapters.StateReady {
		return fmt.Errorf("Unity Editor is not at reviewed identity %s/%s: %s", action.Version, action.Inputs["install_ref"], observation.State)
	}
	reviewedArchitecture, err := editor.reviewedArchitecture()
	if err != nil {
		return err
	}
	result, err := editor.Port.Run(ctx, editor.command(
		"editors", "verify", action.Version,
		"--architecture", reviewedArchitecture,
		"--format", "json",
	))
	if err != nil {
		return err
	}
	var envelope struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &envelope); err != nil || !envelope.Success {
		return errors.New("Unity CLI Editor verification failed")
	}
	return nil
}

func (editor UnityEditor) command(arguments ...string) transport.Command {
	return transport.Command{
		Executable: editor.executable(),
		Arguments: append(
			[]string{"--no-banner", "--no-pager", "--non-interactive"},
			arguments...,
		),
		Environment: map[string]string{
			"UNITY_NO_CONSENT_PROMPT": "1",
			"UNITY_NO_CRASH_REPORT":   "1",
			"UNITY_NO_UPDATE_CHECK":   "1",
		},
	}
}

func (editor UnityEditor) reviewedArchitecture() (string, error) {
	switch {
	case editor.Platform == "darwin" && editor.Architecture == "arm64":
		return "arm64", nil
	case editor.Platform == "windows" && editor.Architecture == "amd64":
		return "x86_64", nil
	default:
		return "", fmt.Errorf(
			"Unity Editor target %s/%s is outside the reviewed Apple Silicon macOS and x64 Windows contract",
			editor.Platform,
			editor.Architecture,
		)
	}
}

func matchesUnityArchitecture(reviewed, installed string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(installed, "-", "_"))
	switch reviewed {
	case "arm64":
		return normalized == "arm64" || normalized == "aarch64"
	case "x86_64":
		return normalized == "x86_64" || normalized == "amd64" || normalized == "x64"
	default:
		return false
	}
}

func (editor UnityEditor) executable() string {
	if editor.Executable != "" {
		return editor.Executable
	}
	return "unity"
}
