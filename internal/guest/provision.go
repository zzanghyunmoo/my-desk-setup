package guest

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
)

type Spec struct {
	SchemaVersion   int                  `yaml:"schema_version"`
	ID              string               `yaml:"id"`
	Distribution    string               `yaml:"distribution"`
	Release         string               `yaml:"release"`
	SystemdRequired bool                 `yaml:"systemd_required"`
	WSLDistribution string               `yaml:"wsl_distribution"`
	Images          map[string]ImageSpec `yaml:"images"`
}

type ImageSpec struct {
	URL    string `yaml:"url"`
	SHA256 string `yaml:"sha256"`
}

type StepStatus string

const (
	StepReady          StepStatus = "ready"
	StepActionRequired StepStatus = "action-required"
)

type Step struct {
	ID         string     `json:"id"`
	Status     StepStatus `json:"status"`
	Executable string     `json:"executable,omitempty"`
	Arguments  []string   `json:"arguments,omitempty"`
	Reason     string     `json:"reason,omitempty"`
}

func LoadSpec(path string) (Spec, error) {
	file, err := os.Open(path)
	if err != nil {
		return Spec{}, fmt.Errorf("open guest spec: %w", err)
	}
	defer file.Close()

	var spec Spec
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&spec); err != nil {
		return Spec{}, fmt.Errorf("decode guest spec: %w", err)
	}
	if spec.SchemaVersion != 1 || spec.ID == "" || spec.Distribution == "" || spec.Release == "" {
		return Spec{}, errors.New("guest spec requires schema_version 1, id, distribution, and release")
	}
	for architecture, image := range spec.Images {
		if image.URL == "" || len(image.SHA256) != 64 {
			return Spec{}, fmt.Errorf("guest image %q requires URL and SHA-256", architecture)
		}
	}
	return spec, nil
}

func Plan(
	host target.Kind,
	name,
	architecture string,
	spec Spec,
) ([]Step, error) {
	if name == "" {
		return nil, errors.New("guest name is required")
	}
	switch host {
	case target.KindMacOSHost:
		image, exists := spec.Images[architecture]
		if !exists {
			return nil, fmt.Errorf("guest image does not support architecture %q", architecture)
		}
		return []Step{
			{
				ID: "ensure-lima", Status: StepReady,
				Executable: "brew", Arguments: []string{"install", "lima"},
			},
			{
				ID: "create-lima-guest", Status: StepReady,
				Executable: "limactl",
				Arguments: []string{
					"create", "--name", name,
					"--set", ".images[0].location=" + image.URL,
					"--set", ".images[0].digest=sha256:" + image.SHA256,
				},
			},
			{
				ID: "start-lima-guest", Status: StepReady,
				Executable: "limactl", Arguments: []string{"start", name},
			},
		}, nil
	case target.KindWindowsHost:
		return []Step{
			{
				ID: "ensure-wsl", Status: StepReady,
				Executable: "wsl.exe", Arguments: []string{"--install", "--no-distribution"},
			},
			{
				ID: "install-wsl-guest", Status: StepReady,
				Executable: "wsl.exe",
				Arguments:  []string{"--install", "--distribution", spec.WSLDistribution},
			},
			{
				ID:     "complete-wsl-first-run",
				Status: StepActionRequired,
				Reason: "A reboot or first Linux user creation may require direct user interaction.",
			},
		}, nil
	default:
		return nil, fmt.Errorf("guest provisioning is host-only, got %q", host)
	}
}

func DockerPreflight(facts target.Facts) error {
	if !facts.SystemdSupported || !facts.SystemdActive {
		return errors.New("action-required: systemd must be supported and active before Docker setup")
	}
	return nil
}
