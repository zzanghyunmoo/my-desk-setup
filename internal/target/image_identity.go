package target

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
)

const ImageIdentityPath = "/etc/mds/image-identity-v1"

type ImageIdentity struct {
	Revision                string
	Provenance              string
	CreationNonceCommitment string
}

func ParseImageIdentity(content []byte) (ImageIdentity, error) {
	if len(content) == 0 || len(content) > 4096 {
		return ImageIdentity{}, errors.New("guest image identity marker has an invalid size")
	}
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	if len(lines) != 4 ||
		lines[0] != "schema=mds.guest-image/v3" ||
		!strings.HasPrefix(lines[1], "image_revision=sha256:") ||
		!strings.HasPrefix(lines[2], "image_provenance=") ||
		!strings.HasPrefix(lines[3], "creation_nonce_commitment=sha256:") {
		return ImageIdentity{}, errors.New("guest image identity marker has an invalid schema")
	}
	revision := strings.TrimPrefix(lines[1], "image_revision=")
	digest := strings.TrimPrefix(revision, "sha256:")
	if artifact.ValidateSHA256(digest) != nil {
		return ImageIdentity{}, errors.New("guest image identity marker has an invalid revision")
	}
	provenance := strings.TrimPrefix(lines[2], "image_provenance=")
	parsed, err := url.ParseRequestURI(provenance)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ImageIdentity{}, errors.New(
			"guest image identity marker has an invalid provenance URL",
		)
	}
	if strings.ContainsAny(provenance, "\r\n\x00") {
		return ImageIdentity{}, fmt.Errorf(
			"guest image identity marker provenance contains control characters",
		)
	}
	commitment := strings.TrimPrefix(lines[3], "creation_nonce_commitment=")
	if ValidateGuestCreationNonceCommitment(commitment) != nil {
		return ImageIdentity{}, errors.New(
			"guest image identity marker has an invalid creation nonce commitment",
		)
	}
	return ImageIdentity{
		Revision: revision, Provenance: provenance,
		CreationNonceCommitment: commitment,
	}, nil
}
