package host

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/guest"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func (runtime GuestRuntime) validateExistingOwnership(
	ctx context.Context,
	action planning.Action,
) (target.ImageIdentity, error) {
	record, err := runtime.validateExistingOwnershipRecord(action)
	if err != nil {
		return target.ImageIdentity{}, err
	}
	return runtime.validateGuestOwnershipMarker(ctx, action, record)
}

func (runtime GuestRuntime) validateExistingOwnershipRecord(
	action planning.Action,
) (guest.Ownership, error) {
	expected, err := runtime.expectedOwnership(action)
	if err != nil {
		return guest.Ownership{}, err
	}
	record, exists, err := guest.LoadOwnership(
		runtime.OwnershipRoot,
		expected.Provider,
		expected.Name,
	)
	if err != nil {
		return guest.Ownership{}, fmt.Errorf("inspect guest ownership: %w", err)
	}
	if !exists {
		return guest.Ownership{}, fmt.Errorf(
			"pre-existing %s guest %q is not owned by mds; choose another name or explicitly resolve the ownership conflict",
			expected.Provider,
			expected.Name,
		)
	}
	if record.ImageURL != expected.ImageURL ||
		record.ImageSHA256 != expected.ImageSHA256 {
		return guest.Ownership{}, fmt.Errorf(
			"managed %s guest %q image provenance conflicts with the current catalog",
			expected.Provider,
			expected.Name,
		)
	}
	if record.Phase != guest.OwnershipCommitted {
		return guest.Ownership{}, fmt.Errorf(
			"%s guest %q has only an uncommitted mds creation intent; it may be externally owned and requires explicit conflict resolution",
			expected.Provider,
			expected.Name,
		)
	}
	return record, nil
}

func (runtime GuestRuntime) requireExistingOwnership(
	ctx context.Context,
	action planning.Action,
) (target.ImageIdentity, error) {
	identity, err := runtime.validateExistingOwnership(ctx, action)
	if err != nil {
		return target.ImageIdentity{}, &adapters.ActionRequiredError{
			Reason: err.Error(),
		}
	}
	return identity, nil
}

func (runtime GuestRuntime) requireOwnershipVacant(
	action planning.Action,
) error {
	expected, err := runtime.expectedOwnership(action)
	if err != nil {
		return err
	}
	record, exists, err := guest.LoadOwnership(
		runtime.OwnershipRoot,
		expected.Provider,
		expected.Name,
	)
	if err != nil {
		return fmt.Errorf("inspect guest ownership: %w", err)
	}
	if exists {
		if record.ImageURL == expected.ImageURL &&
			record.ImageSHA256 == expected.ImageSHA256 {
			return nil
		}
		return &adapters.ActionRequiredError{
			Reason: fmt.Sprintf(
				"mds ownership intent for absent %s guest %q conflicts with the current catalog; inspect the stale record before recreating the guest",
				expected.Provider,
				expected.Name,
			),
		}
	}
	return nil
}

func (runtime GuestRuntime) prepareOwnership(
	action planning.Action,
) (guest.Ownership, error) {
	record, err := runtime.expectedOwnership(action)
	if err != nil {
		return guest.Ownership{}, err
	}
	existing, exists, err := guest.LoadOwnership(
		runtime.OwnershipRoot,
		record.Provider,
		record.Name,
	)
	if err != nil {
		return guest.Ownership{}, fmt.Errorf(
			"inspect guest ownership before publication: %w",
			err,
		)
	}
	if exists {
		if existing.ImageURL == record.ImageURL &&
			existing.ImageSHA256 == record.ImageSHA256 {
			return guest.PrepareOwnership(runtime.OwnershipRoot, record)
		}
		return guest.Ownership{}, errors.New(
			"existing guest ownership intent conflicts with the current catalog",
		)
	}
	return guest.PrepareOwnership(runtime.OwnershipRoot, record)
}

func (runtime GuestRuntime) commitOwnership(record guest.Ownership) error {
	if err := guest.CommitOwnership(runtime.OwnershipRoot, record); err != nil {
		return fmt.Errorf("commit guest ownership after provider creation: %w", err)
	}
	return nil
}

func (runtime GuestRuntime) validateGuestOwnershipMarker(
	ctx context.Context,
	action planning.Action,
	record guest.Ownership,
) (target.ImageIdentity, error) {
	creationNonceCommitment, err := target.GuestCreationNonceCommitment(
		record.CreationNonce,
	)
	if err != nil {
		return target.ImageIdentity{}, errors.New(
			"committed guest creation identity is invalid",
		)
	}
	command, err := runtime.guestImageIdentityReadCommand(
		action,
		creationNonceCommitment,
	)
	if err != nil {
		return target.ImageIdentity{}, err
	}
	result, err := runtime.Port.Run(ctx, command)
	if err != nil {
		return target.ImageIdentity{}, fmt.Errorf(
			"read root-owned guest image identity marker: %w",
			err,
		)
	}
	identity, err := target.ParseImageIdentity([]byte(result.Stdout))
	if err != nil {
		return target.ImageIdentity{}, err
	}
	expectedRevision := "sha256:" + record.ImageSHA256
	if identity.Revision != expectedRevision ||
		identity.Provenance != record.ImageURL {
		return target.ImageIdentity{}, fmt.Errorf(
			"live %s guest %q image identity does not match the committed ownership record",
			record.Provider,
			record.Name,
		)
	}
	if identity.CreationNonceCommitment != creationNonceCommitment {
		return target.ImageIdentity{}, fmt.Errorf(
			"live %s guest %q creation identity does not match the committed ownership record",
			record.Provider,
			record.Name,
		)
	}
	return identity, nil
}

func (runtime GuestRuntime) guestImageIdentityReadCommand(
	action planning.Action,
	expectedCreationNonceCommitment string,
) (transport.Command, error) {
	const script = `set -eu
IFS= read -r expected_creation_nonce_commitment
path=/etc/mds/image-identity-v1
[ -f "$path" ] && [ ! -L "$path" ] || exit 74
metadata=$(/usr/bin/stat -c '%u:%g:%a' "$path")
case "$metadata" in
  0:0:600|0:0:640|0:0:644) ;;
  *) exit 74 ;;
esac
line_count=$(/usr/bin/wc -l < "$path")
[ "$line_count" -eq 4 ] || exit 74
schema=$(/usr/bin/sed -n 's/^schema=//p' "$path")
revision=$(/usr/bin/sed -n 's/^image_revision=//p' "$path")
provenance=$(/usr/bin/sed -n 's/^image_provenance=//p' "$path")
creation_nonce_commitment=$(/usr/bin/sed -n 's/^creation_nonce_commitment=//p' "$path")
[ -n "$schema" ] && [ -n "$revision" ] && [ -n "$provenance" ]
[ "$creation_nonce_commitment" = "$expected_creation_nonce_commitment" ] || exit 74
printf 'schema=%s\n' "$schema"
printf 'image_revision=%s\n' "$revision"
printf 'image_provenance=%s\n' "$provenance"
printf 'creation_nonce_commitment=%s\n' "$creation_nonce_commitment"
`
	guestCommand := transport.Command{
		Executable:  "/bin/sh",
		Arguments:   []string{"-eu", "-c", script, "mds-image-identity"},
		Stdin:       []byte(expectedCreationNonceCommitment + "\n"),
		Timeout:     30 * time.Second,
		OutputLimit: 4096,
	}
	var executable string
	var arguments []string
	switch action.ComponentID {
	case "lima":
		executable, arguments = transport.LimaArgv(
			defaultGuestName,
			guestCommand,
		)
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
		Executable: executable, Arguments: arguments,
		Stdin:   guestCommand.Stdin,
		Timeout: guestCommand.Timeout, OutputLimit: guestCommand.OutputLimit,
	}, nil
}

func (runtime GuestRuntime) expectedOwnership(
	action planning.Action,
) (guest.Ownership, error) {
	image, exists, err := runtime.expectedImage(action)
	if err != nil {
		return guest.Ownership{}, err
	}
	if !exists {
		return guest.Ownership{}, fmt.Errorf(
			"pinned %s guest image is required for ownership",
			action.ComponentID,
		)
	}
	name := defaultGuestName
	if action.ComponentID == "wsl" {
		name = runtime.Spec.WSLDistribution
	}
	return guest.Ownership{
		Provider:    action.ComponentID,
		Name:        name,
		ImageURL:    image.URL,
		ImageSHA256: image.SHA256,
	}, nil
}
