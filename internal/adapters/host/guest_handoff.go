package host

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	exactartifact "github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
	"github.com/zzanghyunmoo/my-desk-setup/internal/guest"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func (runtime GuestRuntime) observeGuestHandoff(
	ctx context.Context,
	action planning.Action,
	base adapters.Observation,
	observedImage target.ImageIdentity,
) (adapters.Observation, error) {
	if err := runtime.verifyGuestHandoffIdentity(
		ctx,
		action,
		observedImage,
	); err != nil {
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
	observedImage, err := runtime.validateExistingOwnership(ctx, action)
	if err != nil {
		return err
	}
	return runtime.verifyGuestHandoffIdentity(ctx, action, observedImage)
}

func (runtime GuestRuntime) verifyGuestHandoffIdentity(
	ctx context.Context,
	action planning.Action,
	observedImage target.ImageIdentity,
) error {
	expectedTarget, command, err := runtime.guestHandoffCommand(
		action,
		observedImage,
	)
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
	if err := target.CheckRevision(
		runtime.CLIRevision,
		runtime.CatalogRevision,
		identity.Target.CLIRevision,
		identity.CatalogRevision,
	); err != nil {
		return err
	}
	image, exists, err := runtime.expectedImage(action)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	expectedRevision := "sha256:" + image.SHA256
	if identity.Target.ImageRevision != expectedRevision {
		return fmt.Errorf(
			"guest image revision mismatch: expected=%s observed=%s",
			expectedRevision,
			identity.Target.ImageRevision,
		)
	}
	if identity.Target.ImageProvenance != image.URL {
		return fmt.Errorf(
			"guest image provenance mismatch: expected=%s observed=%s",
			image.URL,
			identity.Target.ImageProvenance,
		)
	}
	if identity.Target.ImageCreationNonceCommitment !=
		observedImage.CreationNonceCommitment {
		return errors.New(
			"guest creation identity is missing or differs from the observed ownership marker",
		)
	}
	return nil
}

func (runtime GuestRuntime) guestHandoffCommand(
	action planning.Action,
	observedImage target.ImageIdentity,
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
		OutputLimit: transport.DefaultOutputLimit,
	}
	if observedImage.Revision != "" || observedImage.Provenance != "" {
		guestCommand.Environment = map[string]string{
			"MDS_IMAGE_REVISION":                  observedImage.Revision,
			"MDS_IMAGE_PROVENANCE":                observedImage.Provenance,
			"MDS_IMAGE_CREATION_NONCE_COMMITMENT": observedImage.CreationNonceCommitment,
		}
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
		OutputLimit: transport.DefaultOutputLimit,
	}, nil
}

func (runtime GuestRuntime) bootstrapGuestMDS(
	ctx context.Context,
	action planning.Action,
) error {
	architecture := normalizeCatalogArchitecture(runtime.Architecture)
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
	sourceMode := "url"
	var stdin []byte
	if len(artifact.Archive) > 0 {
		sourceMode = "stdin"
		stdin = artifact.Archive
	}
	guestCommand := transport.Command{
		Executable: "/bin/sh",
		Arguments: []string{
			"-eu", "-c", string(guestBootstrapScript), "mds-bootstrap",
			sourceMode, artifact.URL, artifact.SHA256,
		},
		Stdin:       stdin,
		Timeout:     10 * time.Minute,
		OutputLimit: transport.DefaultOutputLimit,
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
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return errors.New("artifact URL must be an absolute credential-free HTTPS URL")
	}
	if exactartifact.ValidateSHA256(artifact.SHA256) != nil {
		return errors.New("artifact SHA-256 must contain exactly 64 lowercase hex characters")
	}
	if len(artifact.Archive) > 0 {
		sum := sha256.Sum256(artifact.Archive)
		if hex.EncodeToString(sum[:]) != artifact.SHA256 {
			return errors.New(
				"local artifact bytes do not match the embedded SHA-256",
			)
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
	guestID, _, err := runtime.guestHandoffCommand(
		action,
		target.ImageIdentity{},
	)
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
	if strings.TrimSpace(runtime.OwnershipRoot) == "" {
		return errors.New("guest runtime ownership root is required")
	}
	return nil
}

func (runtime GuestRuntime) expectedImage(
	action planning.Action,
) (guest.ImageSpec, bool, error) {
	architecture := normalizeCatalogArchitecture(runtime.Architecture)
	var images map[string]guest.ImageSpec
	switch action.ComponentID {
	case "lima":
		images = runtime.Spec.Images
	case "wsl":
		images = runtime.Spec.WSLImages
	default:
		return guest.ImageSpec{}, false, fmt.Errorf(
			"unsupported guest runtime component %q",
			action.ComponentID,
		)
	}
	image, exists := images[architecture]
	if !exists {
		if len(images) == 0 {
			return guest.ImageSpec{}, false, nil
		}
		return guest.ImageSpec{}, false, fmt.Errorf(
			"ubuntu guest has no %s image for %q",
			action.ComponentID,
			architecture,
		)
	}
	if err := validateGuestBootstrapArtifact(GuestBootstrapArtifact{
		URL: image.URL, SHA256: image.SHA256,
	}); err != nil {
		return guest.ImageSpec{}, false, fmt.Errorf(
			"invalid pinned Ubuntu %s image: %w",
			action.ComponentID,
			err,
		)
	}
	return image, true, nil
}
