package target

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type Facts struct {
	ID               ID     `json:"id"`
	OS               string `json:"os"`
	OSVersion        string `json:"os_version"`
	Architecture     string `json:"architecture"`
	RuntimeVersion   string `json:"runtime_version,omitempty"`
	ImageRevision    string `json:"image_revision,omitempty"`
	SystemdSupported bool   `json:"systemd_supported"`
	SystemdActive    bool   `json:"systemd_active"`
	Reachable        bool   `json:"reachable"`
	CLIRevision      string `json:"cli_revision,omitempty"`
	CatalogRevision  string `json:"catalog_revision,omitempty"`
}

func (facts Facts) Fingerprint() (string, error) {
	encoded, err := json.Marshal(facts)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
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
