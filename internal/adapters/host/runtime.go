package host

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
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
	defaultGuestName    = "mds"
	guestHandoffTimeout = 30 * time.Second
)

type GuestBootstrapArtifact struct {
	URL     string
	SHA256  string
	Archive []byte
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
	OwnershipRoot      string
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
			if _, err := runtime.validateExistingOwnershipRecord(action); err != nil {
				return adapters.Observation{
					State: adapters.StateConflict, Detail: err.Error(),
				}, nil
			}
			if instance.Reachable {
				observedImage, err := runtime.validateExistingOwnership(
					ctx,
					action,
				)
				if err != nil {
					return adapters.Observation{
						State: adapters.StateConflict, Detail: err.Error(),
					}, nil
				}
				return runtime.observeGuestHandoff(
					ctx,
					action,
					base,
					observedImage,
				)
			}
			return adapters.Observation{
				State:  adapters.StateConflict,
				Detail: "the stopped Lima guest cannot prove its mds creation identity; start it manually, then rerun",
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
		if _, err := runtime.validateExistingOwnershipRecord(action); err != nil {
			return adapters.Observation{
				State: adapters.StateConflict, Detail: err.Error(),
			}, nil
		}
		running, err := runtime.wslRunningDistributions(ctx)
		if err != nil {
			return adapters.Observation{
				State: adapters.StateConflict, Detail: err.Error(),
			}, nil
		}
		if !hasTarget(
			running,
			target.KindWSLGuest,
			runtime.Spec.WSLDistribution,
		) {
			return adapters.Observation{
				State:  adapters.StateConflict,
				Detail: "the stopped Ubuntu WSL guest cannot prove its mds creation identity; launch it manually, then rerun",
			}, nil
		}
		observedImage, err := runtime.validateExistingOwnership(ctx, action)
		if err != nil {
			return adapters.Observation{
				State: adapters.StateConflict, Detail: err.Error(),
			}, nil
		}
		return runtime.observeGuestHandoff(ctx, action, base, observedImage)
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
		if _, err := runtime.validateExistingOwnershipRecord(action); err != nil {
			return &adapters.ActionRequiredError{Reason: err.Error()}
		}
		if !instance.Reachable {
			return &adapters.ActionRequiredError{
				Reason: "start the stopped Lima guest manually so mds can verify its root-owned creation identity, then rerun",
			}
		}
		if _, err := runtime.requireExistingOwnership(ctx, action); err != nil {
			return err
		}
		return nil
	}
	if err := runtime.requireOwnershipVacant(action); err != nil {
		return err
	}
	record, err := runtime.prepareOwnership(action)
	if err != nil {
		return fmt.Errorf("prepare Lima Ubuntu guest ownership: %w", err)
	}
	image, _, err := runtime.expectedImage(action)
	if err != nil {
		return err
	}
	creationNonceCommitment, err := target.GuestCreationNonceCommitment(
		record.CreationNonce,
	)
	if err != nil {
		return fmt.Errorf("prepare Lima guest creation commitment: %w", err)
	}
	create, err := transport.LimaCreateCommand(
		defaultGuestName,
		runtime.Architecture,
		image.URL,
		image.SHA256,
		creationNonceCommitment,
	)
	if err != nil {
		return fmt.Errorf("build Lima Ubuntu guest template: %w", err)
	}
	if _, err := runtime.Port.Run(ctx, create); err != nil {
		return fmt.Errorf("create Lima Ubuntu guest: %w", err)
	}
	if _, err := runtime.Port.Run(ctx, transport.Command{
		Executable: "limactl",
		Arguments:  []string{"start", defaultGuestName},
		Timeout:    15 * time.Minute,
	}); err != nil {
		return fmt.Errorf("start Lima Ubuntu guest: %w", err)
	}
	if _, err := runtime.validateGuestOwnershipMarker(
		ctx,
		action,
		record,
	); err != nil {
		return fmt.Errorf("verify created Lima guest ownership: %w", err)
	}
	return runtime.commitOwnership(record)
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
			Timeout:    45 * time.Minute,
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
	exists := hasTarget(
		distributions,
		target.KindWSLGuest,
		runtime.Spec.WSLDistribution,
	)
	if exists {
		if _, err := runtime.validateExistingOwnershipRecord(action); err != nil {
			return &adapters.ActionRequiredError{Reason: err.Error()}
		}
		running, err := runtime.wslRunningDistributions(ctx)
		if err != nil {
			return err
		}
		if !hasTarget(
			running,
			target.KindWSLGuest,
			runtime.Spec.WSLDistribution,
		) {
			return &adapters.ActionRequiredError{
				Reason: "launch the stopped Ubuntu WSL guest manually so mds can verify its root-owned creation identity, then rerun",
			}
		}
		if _, err := runtime.requireExistingOwnership(ctx, action); err != nil {
			return err
		}
	} else {
		if err := runtime.requireOwnershipVacant(action); err != nil {
			return err
		}
		record, err := runtime.prepareOwnership(action)
		if err != nil {
			return fmt.Errorf("prepare Ubuntu WSL guest ownership: %w", err)
		}
		if err := runtime.installPinnedWSLImage(
			ctx,
			record.CreationNonce,
		); err != nil {
			return err
		}
		if _, err := runtime.validateGuestOwnershipMarker(
			ctx,
			action,
			record,
		); err != nil {
			return fmt.Errorf("verify created Ubuntu WSL guest ownership: %w", err)
		}
		if err := runtime.commitOwnership(record); err != nil {
			return err
		}
	}
	if _, err := runtime.Port.Run(ctx, transport.Command{
		Executable: "wsl.exe",
		Arguments: []string{
			"--distribution", runtime.Spec.WSLDistribution,
			"--exec", "/bin/sh", "-eu", "-c",
			`uid=$(/usr/bin/id -u)
[ "$uid" -ne 0 ]
entry=$(/usr/bin/getent passwd "$uid")
home=$(printf '%s\n' "$entry" | /usr/bin/cut -d: -f6)
[ -n "$home" ] && [ "$home" != /root ] && [ "$HOME" = "$home" ]
`,
			"mds-default-user",
		},
		Timeout: 5 * time.Minute,
	}); err != nil {
		return &adapters.ActionRequiredError{
			Reason: "launch Ubuntu once to create the Linux user, then rerun the same apply",
		}
	}
	return nil
}

func (runtime GuestRuntime) wslImageIdentityCommand(
	image guest.ImageSpec,
	creationNonceCommitment string,
) transport.Command {
	const script = `umask 022
IFS= read -r creation_nonce_commitment
/usr/bin/install -d -m 0755 /etc/mds
temporary=$(/usr/bin/mktemp /etc/mds/.image-identity-v1.XXXXXX)
cleanup() {
  /bin/rm -f "$temporary"
}
trap cleanup EXIT HUP INT TERM
{
  printf 'schema=mds.guest-image/v3\n'
  printf 'image_revision=%s\n' "$1"
  printf 'image_provenance=%s\n' "$2"
  printf 'creation_nonce_commitment=%s\n' "$creation_nonce_commitment"
} > "$temporary"
/bin/chown 0:0 "$temporary"
/bin/chmod 0644 "$temporary"
/bin/mv -f "$temporary" /etc/mds/image-identity-v1
temporary=
`
	return transport.Command{
		Executable: "wsl.exe",
		Arguments: []string{
			"--distribution", runtime.Spec.WSLDistribution,
			"--user", "root",
			"--exec", "/bin/sh", "-eu", "-c", script,
			"mds-image-identity",
			"sha256:" + image.SHA256,
			image.URL,
		},
		Stdin:   []byte(creationNonceCommitment + "\n"),
		Timeout: 5 * time.Minute,
	}
}

func (runtime GuestRuntime) installPinnedWSLImage(
	ctx context.Context,
	creationNonce string,
) (resultErr error) {
	architecture := normalizeCatalogArchitecture(runtime.Architecture)
	image, exists := runtime.Spec.WSLImages[architecture]
	if !exists {
		return fmt.Errorf("ubuntu WSL image is not pinned for %q", architecture)
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
	client, err := packages.ReviewedHTTPClient(
		runtime.Client,
		image.URL,
		30*time.Minute,
	)
	if err != nil {
		return fmt.Errorf("validate Ubuntu WSL image URL: %w", err)
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
		Timeout: 45 * time.Minute,
	}); err != nil {
		return &adapters.ActionRequiredError{
			Reason: fmt.Sprintf(
				"verified Ubuntu WSL image installation did not complete: %v; finish any WSL reboot/installation prompt, then rerun the same apply",
				err,
			),
		}
	}
	creationNonceCommitment, err := target.GuestCreationNonceCommitment(
		creationNonce,
	)
	if err != nil {
		return fmt.Errorf("prepare WSL guest creation commitment: %w", err)
	}
	if _, err := runtime.Port.Run(
		ctx,
		runtime.wslImageIdentityCommand(image, creationNonceCommitment),
	); err != nil {
		return fmt.Errorf(
			"publish root-owned Ubuntu WSL image identity marker: %w",
			err,
		)
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

func (runtime GuestRuntime) wslRunningDistributions(
	ctx context.Context,
) ([]target.Facts, error) {
	result, err := runtime.Port.Run(ctx, transport.Command{
		Executable: "wsl.exe",
		Arguments:  []string{"--list", "--running", "--quiet"},
	})
	if err != nil {
		return nil, fmt.Errorf("list running WSL guests: %w", err)
	}
	return target.ParseWSLDistributions([]byte(result.Stdout))
}

func normalizeCatalogArchitecture(architecture string) string {
	switch architecture {
	case "x86_64":
		return "amd64"
	case "aarch64":
		return "arm64"
	default:
		return architecture
	}
}

func hasTarget(facts []target.Facts, kind target.Kind, name string) bool {
	for _, item := range facts {
		if item.ID.Kind == kind && item.ID.Name == name {
			return true
		}
	}
	return false
}
