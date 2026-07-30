package doctor

import "github.com/zzanghyunmoo/my-desk-setup/internal/target"

const SchemaVersion = "mds.doctor/v1"

type Report struct {
	SchemaVersion   string       `json:"schema_version"`
	CatalogRevision string       `json:"catalog_revision"`
	Target          target.Facts `json:"target"`
	Ready           bool         `json:"ready"`
	Checks          []Check      `json:"checks"`
}

type Check struct {
	ActionID         string `json:"action_id"`
	ComponentID      string `json:"component_id"`
	Status           string `json:"status"`
	ReasonCode       string `json:"reason_code"`
	RequestedVersion string `json:"requested_version"`
	InstalledVersion string `json:"installed_version,omitempty"`
	VerifiedVersion  string `json:"verified_version,omitempty"`
	Reason           string `json:"reason,omitempty"`
	RecoveryHint     string `json:"recovery_hint,omitempty"`
}
