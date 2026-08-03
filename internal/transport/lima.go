package transport

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
)

type Lima struct {
	Instance string
	executor Executor
}

func NewLima(instance string) (Lima, error) {
	instance = strings.TrimSpace(instance)
	if instance == "" || strings.ContainsAny(instance, "\r\n\x00") {
		return Lima{}, errors.New("valid Lima instance is required")
	}
	return Lima{Instance: instance, executor: Executor{}}, nil
}

func (lima Lima) Run(ctx context.Context, command Command) (Result, error) {
	executable, arguments := LimaArgv(lima.Instance, command)
	return lima.executor.Run(
		ctx,
		executable,
		arguments,
		command.Stdin,
		nil,
		"",
		command.Timeout,
		command.OutputLimit,
	)
}

func LimaArgv(instance string, command Command) (string, []string) {
	guestExecutable, guestArguments := guestArgv(command)
	arguments := []string{"shell", "--tty=false"}
	if command.WorkingDirectory != "" {
		arguments = append(arguments, "--workdir", command.WorkingDirectory)
	}
	arguments = append(arguments, instance, "--", guestExecutable)
	arguments = append(arguments, guestArguments...)
	return "limactl", arguments
}

// LimaCreateCommand creates an instance from a complete one-image template on
// stdin. It avoids mutating whichever image entry a Lima default template
// happens to expose.
func LimaCreateCommand(
	instance,
	architecture,
	imageURL,
	imageSHA256,
	creationNonceCommitment string,
) (Command, error) {
	if _, err := NewLima(instance); err != nil {
		return Command{}, err
	}
	limaArchitecture, err := normalizeLimaArchitecture(architecture)
	if err != nil {
		return Command{}, err
	}
	parsedURL, err := url.ParseRequestURI(imageURL)
	if err != nil ||
		parsedURL.Scheme != "https" ||
		parsedURL.Host == "" ||
		parsedURL.User != nil {
		return Command{}, errors.New("Lima image URL must be an absolute credential-free HTTPS URL")
	}
	if artifact.ValidateSHA256(imageSHA256) != nil {
		return Command{}, errors.New("Lima image SHA-256 must contain exactly 64 lowercase hex characters")
	}
	if !strings.HasPrefix(creationNonceCommitment, "sha256:") ||
		artifact.ValidateSHA256(strings.TrimPrefix(
			creationNonceCommitment,
			"sha256:",
		)) != nil {
		return Command{}, errors.New(
			"Lima guest creation nonce commitment must be sha256 followed by 64 lowercase hex characters",
		)
	}
	template, err := yaml.Marshal(struct {
		Architecture string              `yaml:"arch"`
		Images       []limaTemplateImage `yaml:"images"`
		Provision    []limaProvision     `yaml:"provision"`
	}{
		Architecture: limaArchitecture,
		Images: []limaTemplateImage{{
			Location:     imageURL,
			Architecture: limaArchitecture,
			Digest:       "sha256:" + imageSHA256,
		}},
		Provision: []limaProvision{{
			Mode: "system",
			Script: guestImageIdentityProvision(
				imageURL,
				imageSHA256,
				creationNonceCommitment,
			),
		}},
	})
	if err != nil {
		return Command{}, fmt.Errorf("encode Lima template: %w", err)
	}
	return Command{
		Executable: "limactl",
		Arguments:  []string{"create", "--name", instance, "-"},
		Stdin:      template,
		Timeout:    30 * time.Minute,
	}, nil
}

type limaTemplateImage struct {
	Location     string `yaml:"location"`
	Architecture string `yaml:"arch"`
	Digest       string `yaml:"digest"`
}

type limaProvision struct {
	Mode   string `yaml:"mode"`
	Script string `yaml:"script"`
}

func guestImageIdentityProvision(
	imageURL,
	imageSHA256,
	creationNonceCommitment string,
) string {
	return fmt.Sprintf(`#!/bin/sh
set -eu
umask 022
/usr/bin/install -d -m 0755 /etc/mds
temporary=$(/usr/bin/mktemp /etc/mds/.image-identity-v1.XXXXXX)
cleanup() {
  /bin/rm -f "$temporary"
}
trap cleanup EXIT HUP INT TERM
/bin/cat > "$temporary" <<'MDS_IMAGE_IDENTITY'
schema=mds.guest-image/v3
image_revision=sha256:%s
image_provenance=%s
creation_nonce_commitment=%s
MDS_IMAGE_IDENTITY
/bin/chown 0:0 "$temporary"
/bin/chmod 0644 "$temporary"
/bin/mv -f "$temporary" /etc/mds/image-identity-v1
temporary=
`, imageSHA256, imageURL, creationNonceCommitment)
}

func normalizeLimaArchitecture(architecture string) (string, error) {
	switch architecture {
	case "arm64", "aarch64":
		return "aarch64", nil
	case "amd64", "x86_64":
		return "x86_64", nil
	default:
		return "", fmt.Errorf("unsupported Lima architecture %q", architecture)
	}
}
