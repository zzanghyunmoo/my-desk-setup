package target

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

type Facts struct {
	ID                 ID     `json:"id"`
	OS                 string `json:"os"`
	OSVersion          string `json:"os_version"`
	Architecture       string `json:"architecture"`
	RuntimeVersion     string `json:"runtime_version,omitempty"`
	ImageRevision      string `json:"image_revision,omitempty"`
	ImageProvenance    string `json:"image_provenance,omitempty"`
	ImageCreationNonce string `json:"image_creation_nonce,omitempty"`
	SystemdSupported   bool   `json:"systemd_supported"`
	SystemdActive      bool   `json:"systemd_active"`
	Reachable          bool   `json:"reachable"`
	CLIRevision        string `json:"cli_revision,omitempty"`
	CatalogRevision    string `json:"catalog_revision,omitempty"`
}

func (facts Facts) Fingerprint() (string, error) {
	stable := struct {
		ID                 ID     `json:"id"`
		OS                 string `json:"os"`
		OSVersion          string `json:"os_version"`
		Architecture       string `json:"architecture"`
		RuntimeVersion     string `json:"runtime_version,omitempty"`
		ImageRevision      string `json:"image_revision,omitempty"`
		ImageProvenance    string `json:"image_provenance,omitempty"`
		ImageCreationNonce string `json:"image_creation_nonce,omitempty"`
		CLIRevision        string `json:"cli_revision,omitempty"`
		CatalogRevision    string `json:"catalog_revision,omitempty"`
	}{
		ID: facts.ID, OS: facts.OS, OSVersion: facts.OSVersion,
		Architecture: facts.Architecture, RuntimeVersion: facts.RuntimeVersion,
		ImageRevision: facts.ImageRevision, ImageProvenance: facts.ImageProvenance,
		ImageCreationNonce: facts.ImageCreationNonce,
		CLIRevision:        facts.CLIRevision, CatalogRevision: facts.CatalogRevision,
	}
	encoded, err := json.Marshal(stable)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
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
