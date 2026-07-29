package guest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/packages"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

const (
	dockerKeyURL = "https://download.docker.com/linux/ubuntu/gpg"
	dockerKeySHA = "1500c1f56fa9e26b9b8f42452a553675796ade0807cdce11975eb98170b3a570"
)

type Docker struct {
	Facts    target.Facts
	Port     transport.Port
	Delegate adapters.Component
	Client   *http.Client
	Getenv   func(string) string
	KeyURL   string
	KeySHA   string
	Username func() (string, error)
}

func (docker Docker) Observe(
	ctx context.Context,
	action planning.Action,
) (adapters.Observation, error) {
	getenv := docker.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if endpoint := strings.TrimSpace(getenv("DOCKER_HOST")); endpoint != "" &&
		endpoint != "unix:///var/run/docker.sock" {
		return adapters.Observation{
			State:  adapters.StateConflict,
			Detail: "DOCKER_HOST points outside the guest-local Docker socket",
		}, nil
	}
	if docker.Delegate == nil {
		return adapters.Observation{}, errors.New("Docker delegate is required")
	}
	return docker.Delegate.Observe(ctx, action)
}

func (docker Docker) Apply(
	ctx context.Context,
	action planning.Action,
) error {
	if docker.Port == nil {
		return errors.New("Docker transport port is required")
	}
	if !docker.Facts.SystemdSupported || !docker.Facts.SystemdActive {
		return &adapters.ActionRequiredError{
			Reason: "systemd must be supported and active before Docker installation",
		}
	}
	architecture, err := dockerArchitecture(docker.Facts.Architecture)
	if err != nil {
		return err
	}

	temporaryDirectory, err := os.MkdirTemp("", "mds-docker-repository-*")
	if err != nil {
		return fmt.Errorf("create Docker repository temporary directory: %w", err)
	}
	defer os.RemoveAll(temporaryDirectory)
	keyPath := filepath.Join(temporaryDirectory, "docker.asc")
	listPath := filepath.Join(temporaryDirectory, "docker.list")
	client := docker.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	keyURL := docker.KeyURL
	if keyURL == "" {
		keyURL = dockerKeyURL
	}
	keySHA := docker.KeySHA
	if keySHA == "" {
		keySHA = dockerKeySHA
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, keyURL, nil)
	if err != nil {
		return fmt.Errorf("create Docker key request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download Docker repository key: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download Docker repository key: HTTP %s", response.Status)
	}
	if err := packages.DownloadAndVerify(response.Body, keyPath, keySHA); err != nil {
		return fmt.Errorf("verify Docker repository key: %w", err)
	}
	repository := fmt.Sprintf(
		"deb [arch=%s signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu resolute stable\n",
		architecture,
	)
	if err := os.WriteFile(listPath, []byte(repository), 0o600); err != nil {
		return fmt.Errorf("write Docker repository list: %w", err)
	}

	commands := []transport.Command{
		{Executable: "sudo", Arguments: []string{"-n", "true"}},
		{
			Executable: "sudo",
			Arguments:  []string{"-n", "install", "-d", "-m", "0755", "/etc/apt/keyrings"},
		},
		{
			Executable: "sudo",
			Arguments:  []string{"-n", "install", "-m", "0644", keyPath, "/etc/apt/keyrings/docker.asc"},
		},
		{
			Executable: "sudo",
			Arguments: []string{
				"-n", "install", "-m", "0644", listPath,
				"/etc/apt/sources.list.d/docker.list",
			},
		},
		{
			Executable: "sudo",
			Arguments: []string{
				"-n", "env", "DEBIAN_FRONTEND=noninteractive",
				"apt-get", "update",
			},
		},
	}
	installCommands, err := packages.APTInstall(action)
	if err != nil {
		return err
	}
	commands = append(commands, installCommands[1])
	commands = append(commands, transport.Command{
		Executable: "sudo",
		Arguments:  []string{"-n", "systemctl", "enable", "--now", "docker"},
	})
	for _, command := range commands {
		if _, err := docker.Port.Run(ctx, command); err != nil {
			return fmt.Errorf("configure guest-local Docker: %w", err)
		}
	}

	username := docker.Username
	if username == nil {
		username = func() (string, error) {
			current, err := user.Current()
			if err != nil {
				return "", err
			}
			return current.Username, nil
		}
	}
	currentUsername, err := username()
	if err != nil {
		return fmt.Errorf("resolve current guest user: %w", err)
	}
	if strings.TrimSpace(currentUsername) == "" {
		return errors.New("current guest username is empty")
	}
	groups, err := docker.Port.Run(ctx, transport.Command{
		Executable: "id", Arguments: []string{"-nG", currentUsername},
	})
	if err != nil {
		return fmt.Errorf("inspect Docker group membership: %w", err)
	}
	if !containsWord(groups.Stdout, "docker") {
		if _, err := docker.Port.Run(ctx, transport.Command{
			Executable: "sudo",
			Arguments:  []string{"-n", "usermod", "-aG", "docker", currentUsername},
		}); err != nil {
			return fmt.Errorf("add guest user to Docker group: %w", err)
		}
		return &adapters.ActionRequiredError{
			Reason: "Docker was installed; restart the guest shell so docker group membership becomes active",
		}
	}
	return nil
}

func (docker Docker) Verify(
	ctx context.Context,
	action planning.Action,
) error {
	if docker.Delegate == nil {
		return errors.New("Docker delegate is required")
	}
	if docker.Port == nil {
		return errors.New("Docker transport port is required")
	}
	if err := docker.Delegate.Verify(ctx, action); err != nil {
		return err
	}
	// The delegate already executes the catalog's docker version and
	// hello-world checks. Compose is an additional contract of this adapter.
	if _, err := docker.Port.Run(ctx, transport.Command{
		Executable: "docker",
		Arguments:  []string{"compose", "version"},
	}); err != nil {
		return fmt.Errorf("verify guest-local Docker Compose: %w", err)
	}
	return nil
}

func dockerArchitecture(architecture string) (string, error) {
	switch architecture {
	case "amd64", "x86_64":
		return "amd64", nil
	case "arm64", "aarch64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("Docker repository does not support architecture %q", architecture)
	}
}

func containsWord(value, wanted string) bool {
	for _, word := range strings.Fields(value) {
		if word == wanted {
			return true
		}
	}
	return false
}
