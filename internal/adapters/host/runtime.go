package host

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/packages"
	"github.com/zzanghyunmoo/my-desk-setup/internal/guest"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

//go:embed guest-bootstrap.sh
var guestBootstrapScript []byte

const (
	defaultGuestName        = "mds"
	guestHandoffTimeout     = 30 * time.Second
	guestHandoffOutputLimit = 1 << 20
)

type GuestBootstrapArtifact struct {
	URL    string
	SHA256 string
}

// GuestRuntime owns only host-side WSL/Lima lifecycle. Linux component
// reconciliation still runs through mds inside the guest.
type GuestRuntime struct {
	Architecture       string
	Port               transport.Port
	Delegate           adapters.Component
	Spec               guest.Spec
	CLIRevision        string
	CatalogRevision    string
	BootstrapArtifacts map[string]GuestBootstrapArtifact
	Client             *http.Client
}

func (runtime GuestRuntime) Observe(
	ctx context.Context,
	action planning.Action,
) (adapters.Observation, error) {
	if runtime.Port == nil || runtime.Delegate == nil {
		return adapters.Observation{}, errors.New("guest runtime requires port and delegate")
	}
	if err := runtime.validateRevisions(); err != nil {
		return adapters.Observation{}, err
	}
	base, err := runtime.Delegate.Observe(ctx, action)
	if err != nil || base.State != adapters.StateReady {
		return base, err
	}
	switch action.ComponentID {
	case "lima":
		instances, err := runtime.limaInstances(ctx)
		if err != nil {
			return adapters.Observation{
				State: adapters.StateConflict, Detail: err.Error(),
			}, nil
		}
		for _, instance := range instances {
			if instance.ID.Name != defaultGuestName {
				continue
			}
			if instance.Reachable {
				return runtime.observeGuestHandoff(ctx, action, base)
			}
			return adapters.Observation{
				State:  adapters.StateAbsent,
				Detail: "managed Lima guest exists but is stopped",
			}, nil
		}
		return adapters.Observation{
			State: adapters.StateAbsent, Detail: "managed Lima guest is absent",
		}, nil
	case "wsl":
		distributions, err := runtime.wslDistributions(ctx)
		if err != nil {
			return adapters.Observation{
				State: adapters.StateConflict, Detail: err.Error(),
			}, nil
		}
		if !hasTarget(distributions, target.KindWSLGuest, runtime.Spec.WSLDistribution) {
			return adapters.Observation{
				State: adapters.StateAbsent, Detail: "managed Ubuntu WSL guest is absent",
			}, nil
		}
		if _, err := runtime.Port.Run(ctx, transport.Command{
			Executable: "wsl.exe",
			Arguments: []string{
				"--distribution", runtime.Spec.WSLDistribution,
				"--exec", "true",
			},
		}); err != nil {
			return adapters.Observation{
				State:  adapters.StateAbsent,
				Detail: "managed Ubuntu WSL guest requires first-run completion",
			}, nil
		}
		return runtime.observeGuestHandoff(ctx, action, base)
	default:
		return adapters.Observation{}, fmt.Errorf(
			"unsupported guest runtime component %q",
			action.ComponentID,
		)
	}
}

func (runtime GuestRuntime) Apply(
	ctx context.Context,
	action planning.Action,
) error {
	if runtime.Port == nil || runtime.Delegate == nil {
		return errors.New("guest runtime requires port and delegate")
	}
	if err := runtime.validateRevisions(); err != nil {
		return err
	}
	var err error
	switch action.ComponentID {
	case "lima":
		err = runtime.applyLima(ctx, action)
	case "wsl":
		err = runtime.applyWSL(ctx, action)
	default:
		return fmt.Errorf("unsupported guest runtime component %q", action.ComponentID)
	}
	if err != nil {
		return err
	}
	if err := runtime.verifyGuestHandoff(ctx, action); err == nil {
		return nil
	}
	if err := runtime.bootstrapGuestMDS(ctx, action); err != nil {
		return err
	}
	if err := runtime.verifyGuestHandoff(ctx, action); err != nil {
		return fmt.Errorf(
			"guest-local mds identity did not converge after verified bootstrap: %w",
			err,
		)
	}
	return nil
}

func (runtime GuestRuntime) Verify(
	ctx context.Context,
	action planning.Action,
) error {
	if runtime.Port == nil || runtime.Delegate == nil {
		return errors.New("guest runtime requires port and delegate")
	}
	if err := runtime.validateRevisions(); err != nil {
		return err
	}
	if err := runtime.Delegate.Verify(ctx, action); err != nil {
		return err
	}
	observation, err := runtime.Observe(ctx, action)
	if err != nil {
		return err
	}
	if observation.State != adapters.StateReady {
		if strings.HasPrefix(observation.Detail, "guest-local mds") {
			return &adapters.ActionRequiredError{
				Reason: runtime.guestBootstrapReason(action),
			}
		}
		return fmt.Errorf("guest runtime is not ready: %s", observation.Detail)
	}
	return nil
}

func (runtime GuestRuntime) applyLima(
	ctx context.Context,
	action planning.Action,
) error {
	base, err := runtime.Delegate.Observe(ctx, action)
	if err != nil {
		return err
	}
	if base.State == adapters.StateConflict {
		return errors.New(base.Detail)
	}
	if base.State == adapters.StateAbsent {
		if err := runtime.Delegate.Apply(ctx, action); err != nil {
			return err
		}
	}
	instances, err := runtime.limaInstances(ctx)
	if err != nil {
		return err
	}
	for _, instance := range instances {
		if instance.ID.Name != defaultGuestName {
			continue
		}
		if instance.Reachable {
			return nil
		}
		_, err := runtime.Port.Run(ctx, transport.Command{
			Executable: "limactl",
			Arguments:  []string{"start", defaultGuestName},
		})
		return err
	}
	image, exists := runtime.Spec.Images[runtime.Architecture]
	if !exists {
		return fmt.Errorf("ubuntu guest has no image for %q", runtime.Architecture)
	}
	for _, command := range []transport.Command{
		{
			Executable: "limactl",
			Arguments: []string{
				"create", "--name", defaultGuestName,
				"--set", ".images[0].location=" + image.URL,
				"--set", ".images[0].digest=sha256:" + image.SHA256,
			},
		},
		{
			Executable: "limactl",
			Arguments:  []string{"start", defaultGuestName},
		},
	} {
		if _, err := runtime.Port.Run(ctx, command); err != nil {
			return fmt.Errorf("prepare Lima Ubuntu guest: %w", err)
		}
	}
	return nil
}

func (runtime GuestRuntime) applyWSL(
	ctx context.Context,
	action planning.Action,
) error {
	base, err := runtime.Delegate.Observe(ctx, action)
	if err != nil {
		return err
	}
	if base.State == adapters.StateConflict {
		return errors.New(base.Detail)
	}
	if base.State == adapters.StateAbsent {
		if _, err := runtime.Port.Run(ctx, transport.Command{
			Executable: "wsl.exe",
			Arguments:  []string{"--install", "--no-distribution"},
		}); err != nil {
			return &adapters.ActionRequiredError{
				Reason: "enable WSL, reboot Windows if requested, then rerun the same apply",
			}
		}
	}
	distributions, err := runtime.wslDistributions(ctx)
	if err != nil {
		return err
	}
	if !hasTarget(distributions, target.KindWSLGuest, runtime.Spec.WSLDistribution) {
		if err := runtime.installPinnedWSLImage(ctx); err != nil {
			return err
		}
	}
	if _, err := runtime.Port.Run(ctx, transport.Command{
		Executable: "wsl.exe",
		Arguments: []string{
			"--distribution", runtime.Spec.WSLDistribution,
			"--exec", "true",
		},
	}); err != nil {
		return &adapters.ActionRequiredError{
			Reason: "launch Ubuntu once to create the Linux user, then rerun the same apply",
		}
	}
	return nil
}

func (runtime GuestRuntime) installPinnedWSLImage(ctx context.Context) (resultErr error) {
	architecture := runtime.Architecture
	switch architecture {
	case "x86_64":
		architecture = "amd64"
	case "aarch64":
		architecture = "arm64"
	}
	image, exists := runtime.Spec.WSLImages[architecture]
	if !exists {
		return fmt.Errorf("Ubuntu WSL image is not pinned for %q", architecture)
	}
	if err := validateGuestBootstrapArtifact(GuestBootstrapArtifact{
		URL: image.URL, SHA256: image.SHA256,
	}); err != nil {
		return fmt.Errorf("invalid pinned Ubuntu WSL image: %w", err)
	}
	temporaryDirectory, err := os.MkdirTemp("", "mds-wsl-image-*")
	if err != nil {
		return fmt.Errorf("create WSL image temporary directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(temporaryDirectory); err != nil {
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("remove WSL image temporary directory: %w", err),
			)
		}
	}()
	imagePath := filepath.Join(temporaryDirectory, "ubuntu-26.04.wsl")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, image.URL, nil)
	if err != nil {
		return fmt.Errorf("create Ubuntu WSL image request: %w", err)
	}
	client := runtime.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download Ubuntu WSL image: %w", err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("close Ubuntu WSL image response: %w", err),
			)
		}
	}()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download Ubuntu WSL image: HTTP %s", response.Status)
	}
	if err := packages.DownloadAndVerify(
		response.Body,
		imagePath,
		image.SHA256,
	); err != nil {
		return fmt.Errorf("verify Ubuntu WSL image: %w", err)
	}
	if _, err := runtime.Port.Run(ctx, transport.Command{
		Executable: "wsl.exe",
		Arguments: []string{
			"--install",
			"--from-file", imagePath,
			"--name", runtime.Spec.WSLDistribution,
			"--no-launch",
		},
	}); err != nil {
		return &adapters.ActionRequiredError{
			Reason: fmt.Sprintf(
				"verified Ubuntu WSL image installation did not complete: %v; finish any WSL reboot/installation prompt, then rerun the same apply",
				err,
			),
		}
	}
	return nil
}

func (runtime GuestRuntime) limaInstances(ctx context.Context) ([]target.Facts, error) {
	result, err := runtime.Port.Run(ctx, transport.Command{
		Executable: "limactl",
		Arguments:  []string{"list", "--json"},
	})
	if err != nil {
		return nil, fmt.Errorf("list Lima guests: %w", err)
	}
	return target.ParseLimaInstances([]byte(result.Stdout))
}

func (runtime GuestRuntime) wslDistributions(ctx context.Context) ([]target.Facts, error) {
	result, err := runtime.Port.Run(ctx, transport.Command{
		Executable: "wsl.exe",
		Arguments:  []string{"--list", "--quiet"},
	})
	if err != nil {
		return nil, fmt.Errorf("list WSL guests: %w", err)
	}
	return target.ParseWSLDistributions([]byte(result.Stdout))
}

func (runtime GuestRuntime) observeGuestHandoff(
	ctx context.Context,
	action planning.Action,
	base adapters.Observation,
) (adapters.Observation, error) {
	if err := runtime.verifyGuestHandoff(ctx, action); err != nil {
		return adapters.Observation{
			State:  adapters.StateAbsent,
			Detail: "guest-local mds is missing or stale: " + err.Error(),
		}, nil
	}
	return base, nil
}

func (runtime GuestRuntime) verifyGuestHandoff(
	ctx context.Context,
	action planning.Action,
) error {
	expectedTarget, command, err := runtime.guestHandoffCommand(action)
	if err != nil {
		return err
	}
	result, err := runtime.Port.Run(ctx, command)
	if err != nil {
		return fmt.Errorf("run guest-local mds handoff: %w", err)
	}
	var identity struct {
		CatalogRevision string       `json:"catalog_revision"`
		Target          target.Facts `json:"target"`
	}
	if err := json.Unmarshal(
		[]byte(strings.TrimSpace(result.Stdout)),
		&identity,
	); err != nil {
		return fmt.Errorf("decode guest-local mds plan identity: %w", err)
	}
	if identity.Target.ID != expectedTarget {
		return fmt.Errorf(
			"guest-local mds target mismatch: expected=%s observed=%s",
			expectedTarget.String(),
			identity.Target.ID.String(),
		)
	}
	if identity.Target.CatalogRevision != identity.CatalogRevision {
		return fmt.Errorf(
			"guest-local mds catalog identity is inconsistent: plan=%s target=%s",
			identity.CatalogRevision,
			identity.Target.CatalogRevision,
		)
	}
	return target.CheckRevision(
		runtime.CLIRevision,
		runtime.CatalogRevision,
		identity.Target.CLIRevision,
		identity.CatalogRevision,
	)
}

func (runtime GuestRuntime) guestHandoffCommand(
	action planning.Action,
) (target.ID, transport.Command, error) {
	var (
		guestID    target.ID
		executable string
		arguments  []string
		err        error
	)
	guestCommand := transport.Command{
		Executable: "/bin/sh",
		Arguments: []string{
			"-c", `exec "$HOME/.local/bin/mds" "$@"`, "mds",
		},
		Timeout:     guestHandoffTimeout,
		OutputLimit: guestHandoffOutputLimit,
	}
	switch action.ComponentID {
	case "lima":
		guestID, err = target.NewID(target.KindLimaGuest, defaultGuestName)
		if err == nil {
			guestCommand.Arguments = append(
				guestCommand.Arguments,
				guestPlanArguments(guestID)...,
			)
			executable, arguments = transport.LimaArgv(defaultGuestName, guestCommand)
		}
	case "wsl":
		guestID, err = target.NewID(
			target.KindWSLGuest,
			runtime.Spec.WSLDistribution,
		)
		if err == nil {
			guestCommand.Arguments = append(
				guestCommand.Arguments,
				guestPlanArguments(guestID)...,
			)
			executable, arguments = transport.WSLArgv(
				runtime.Spec.WSLDistribution,
				guestCommand,
			)
		}
	default:
		return target.ID{}, transport.Command{}, fmt.Errorf(
			"unsupported guest runtime component %q",
			action.ComponentID,
		)
	}
	if err != nil {
		return target.ID{}, transport.Command{}, err
	}
	return guestID, transport.Command{
		Executable:  executable,
		Arguments:   arguments,
		Timeout:     guestHandoffTimeout,
		OutputLimit: guestHandoffOutputLimit,
	}, nil
}

func (runtime GuestRuntime) bootstrapGuestMDS(
	ctx context.Context,
	action planning.Action,
) error {
	architecture := runtime.Architecture
	switch architecture {
	case "x86_64":
		architecture = "amd64"
	case "aarch64":
		architecture = "arm64"
	}
	artifact, exists := runtime.BootstrapArtifacts[architecture]
	if !exists {
		return &adapters.ActionRequiredError{
			Reason: runtime.guestBootstrapReason(action),
		}
	}
	if err := validateGuestBootstrapArtifact(artifact); err != nil {
		return fmt.Errorf("invalid embedded guest bootstrap artifact: %w", err)
	}
	command, err := runtime.guestBootstrapCommand(action, artifact)
	if err != nil {
		return err
	}
	result, err := runtime.Port.Run(ctx, command)
	if err == nil {
		return nil
	}
	if result.ExitCode == 73 {
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = "guest-local mds is not owned by mds"
		}
		return &adapters.ActionRequiredError{
			Reason: detail + "; resolve the ownership conflict, then rerun the same mds apply",
		}
	}
	return fmt.Errorf("bootstrap guest-local mds from verified release artifact: %w", err)
}

func (runtime GuestRuntime) guestBootstrapCommand(
	action planning.Action,
	artifact GuestBootstrapArtifact,
) (transport.Command, error) {
	guestCommand := transport.Command{
		Executable: "/bin/sh",
		Arguments: []string{
			"-eu", "-s", "--", artifact.URL, artifact.SHA256,
		},
		Stdin:       append([]byte(nil), guestBootstrapScript...),
		Timeout:     2 * time.Minute,
		OutputLimit: guestHandoffOutputLimit,
	}
	var executable string
	var arguments []string
	switch action.ComponentID {
	case "lima":
		executable, arguments = transport.LimaArgv(defaultGuestName, guestCommand)
	case "wsl":
		executable, arguments = transport.WSLArgv(
			runtime.Spec.WSLDistribution,
			guestCommand,
		)
	default:
		return transport.Command{}, fmt.Errorf(
			"unsupported guest runtime component %q",
			action.ComponentID,
		)
	}
	return transport.Command{
		Executable:  executable,
		Arguments:   arguments,
		Stdin:       guestCommand.Stdin,
		Timeout:     guestCommand.Timeout,
		OutputLimit: guestCommand.OutputLimit,
	}, nil
}

func validateGuestBootstrapArtifact(artifact GuestBootstrapArtifact) error {
	parsed, err := url.ParseRequestURI(artifact.URL)
	if err != nil ||
		parsed.Scheme != "https" ||
		parsed.Host == "" ||
		parsed.User != nil {
		return errors.New("artifact URL must be an absolute credential-free HTTPS URL")
	}
	if len(artifact.SHA256) != 64 {
		return errors.New("artifact SHA-256 must contain exactly 64 lowercase hex characters")
	}
	for _, character := range artifact.SHA256 {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return errors.New("artifact SHA-256 must contain exactly 64 lowercase hex characters")
		}
	}
	return nil
}

func guestPlanArguments(id target.ID) []string {
	return []string{
		"plan",
		"--target", id.String(),
		"--all",
		"--format", "json",
	}
}

func (runtime GuestRuntime) guestBootstrapReason(action planning.Action) string {
	guestID, _, err := runtime.guestHandoffCommand(action)
	if err != nil {
		return "guest-local mds bootstrap is required"
	}
	return fmt.Sprintf(
		"guest-local mds bootstrap is required in %s, but this host CLI has no reviewed Linux/%s artifact URL and SHA-256; install a release build containing guest bootstrap metadata for CLI revision %q and catalog revision %q, then rerun",
		guestID.String(),
		runtime.Architecture,
		runtime.CLIRevision,
		runtime.CatalogRevision,
	)
}

func (runtime GuestRuntime) validateRevisions() error {
	if strings.TrimSpace(runtime.CLIRevision) == "" {
		return errors.New("guest runtime CLI revision is required")
	}
	if strings.TrimSpace(runtime.CatalogRevision) == "" {
		return errors.New("guest runtime catalog revision is required")
	}
	return nil
}

func hasTarget(facts []target.Facts, kind target.Kind, name string) bool {
	for _, item := range facts {
		if item.ID.Kind == kind && item.ID.Name == name {
			return true
		}
	}
	return false
}
