package target

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
)

const guestCreationNonceCommitmentDomain = "mds.guest-creation-nonce/v1\x00"

type Facts struct {
	ID                           ID     `json:"id"`
	OS                           string `json:"os"`
	OSVersion                    string `json:"os_version"`
	Architecture                 string `json:"architecture"`
	RuntimeVersion               string `json:"runtime_version,omitempty"`
	ImageRevision                string `json:"image_revision,omitempty"`
	ImageProvenance              string `json:"image_provenance,omitempty"`
	ImageCreationNonceCommitment string `json:"image_creation_nonce_commitment,omitempty"`
	SystemdSupported             bool   `json:"systemd_supported"`
	SystemdActive                bool   `json:"systemd_active"`
	Reachable                    bool   `json:"reachable"`
	CLIRevision                  string `json:"cli_revision,omitempty"`
	CatalogRevision              string `json:"catalog_revision,omitempty"`
}

func (facts Facts) Fingerprint() (string, error) {
	stable := struct {
		ID                           ID     `json:"id"`
		OS                           string `json:"os"`
		OSVersion                    string `json:"os_version"`
		Architecture                 string `json:"architecture"`
		RuntimeVersion               string `json:"runtime_version,omitempty"`
		ImageRevision                string `json:"image_revision,omitempty"`
		ImageProvenance              string `json:"image_provenance,omitempty"`
		ImageCreationNonceCommitment string `json:"image_creation_nonce_commitment,omitempty"`
		CLIRevision                  string `json:"cli_revision,omitempty"`
		CatalogRevision              string `json:"catalog_revision,omitempty"`
	}{
		ID: facts.ID, OS: facts.OS, OSVersion: facts.OSVersion,
		Architecture: facts.Architecture, RuntimeVersion: facts.RuntimeVersion,
		ImageRevision: facts.ImageRevision, ImageProvenance: facts.ImageProvenance,
		ImageCreationNonceCommitment: facts.ImageCreationNonceCommitment,
		CLIRevision:                  facts.CLIRevision,
		CatalogRevision:              facts.CatalogRevision,
	}
	encoded, err := json.Marshal(stable)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func GuestCreationNonceCommitment(nonce string) (string, error) {
	if artifact.ValidateSHA256(nonce) != nil {
		return "", errors.New(
			"guest creation nonce must contain exactly 64 lowercase hex characters",
		)
	}
	decoded, err := hex.DecodeString(nonce)
	if err != nil {
		return "", errors.New(
			"guest creation nonce must contain exactly 64 lowercase hex characters",
		)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(guestCreationNonceCommitmentDomain))
	_, _ = hash.Write(decoded)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func ValidateGuestCreationNonceCommitment(commitment string) error {
	const prefix = "sha256:"
	if len(commitment) != len(prefix)+sha256.Size*2 ||
		commitment[:len(prefix)] != prefix {
		return errors.New("guest creation nonce commitment is invalid")
	}
	if artifact.ValidateSHA256(commitment[len(prefix):]) != nil {
		return errors.New("guest creation nonce commitment is invalid")
	}
	return nil
}

// ApplyPreflight validates facts that can change independently of stable target
// identity and therefore must be checked immediately before mutation.
func (facts Facts) ApplyPreflight() error {
	if !facts.Reachable {
		return errors.New("target is not reachable")
	}
	if facts.OS == "linux" && facts.SystemdSupported && !facts.SystemdActive {
		return errors.New("target systemd is not active")
	}
	return nil
}

func CheckRevision(
	localCLI,
	localCatalog,
	remoteCLI,
	remoteCatalog string,
) error {
	if localCLI != remoteCLI {
		return &RevisionMismatchError{
			Field: "cli", Local: localCLI, Remote: remoteCLI,
		}
	}
	if localCatalog != remoteCatalog {
		return &RevisionMismatchError{
			Field: "catalog", Local: localCatalog, Remote: remoteCatalog,
		}
	}
	return nil
}

type RevisionMismatchError struct {
	Field  string
	Local  string
	Remote string
}

func (err *RevisionMismatchError) Error() string {
	return "stale guest " + err.Field + " revision: local=" + err.Local + " remote=" + err.Remote
}
