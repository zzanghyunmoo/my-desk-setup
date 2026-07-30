package evidence

import (
	"encoding/json"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
)

const (
	SchemaVersion           = "mds.target-evidence/v1"
	CaptureKindActualTarget = "actual-target"

	ManifestFile  = "manifest.json"
	PlanFile      = "plan.json"
	DoctorFile    = "doctor.json"
	ChecksumsFile = "checksums.txt"
)

type Status string

const (
	StatusImplemented Status = "implemented"
	StatusBlocked     Status = "blocked"
	StatusVerified    Status = "verified"
)

type Manifest struct {
	SchemaVersion   string           `json:"schema_version"`
	CaptureKind     string           `json:"capture_kind"`
	Status          Status           `json:"status"`
	CapturedAtUnix  json.Number      `json:"captured_at_unix"`
	Target          TargetIdentity   `json:"target"`
	CLI             CLIIdentity      `json:"cli"`
	BinarySHA256    string           `json:"binary_sha256"`
	CatalogRevision string           `json:"catalog_revision"`
	PlanDigest      string           `json:"plan_digest"`
	Components      []ComponentCheck `json:"components"`
}

type TargetIdentity struct {
	ID          string      `json:"id"`
	Kind        target.Kind `json:"kind"`
	Fingerprint string      `json:"fingerprint"`
}

type CLIIdentity struct {
	Version  string `json:"version"`
	Commit   string `json:"commit"`
	Date     string `json:"date"`
	Revision string `json:"revision"`
}

type ComponentCheck struct {
	ActionID         string `json:"action_id"`
	ComponentID      string `json:"component_id"`
	Status           string `json:"status"`
	ReasonCode       string `json:"reason_code"`
	RequestedVersion string `json:"requested_version"`
	InstalledVersion string `json:"installed_version,omitempty"`
	VerifiedVersion  string `json:"verified_version,omitempty"`
}

// DoctorSnapshot is intentionally bounded. It omits free-form reason and
// recovery text because those fields can contain machine-local paths.
type DoctorSnapshot struct {
	SchemaVersion   string           `json:"schema_version"`
	CatalogRevision string           `json:"catalog_revision"`
	Target          TargetIdentity   `json:"target"`
	CLIRevision     string           `json:"cli_revision"`
	Ready           bool             `json:"ready"`
	Checks          []ComponentCheck `json:"checks"`
}

type CertifyRequest struct {
	MDSPath    string
	TargetID   string
	OutputDir  string
	All        bool
	Profile    string
	Components []string
	// ExpectedBinarySHA256 binds capture to a release artifact before the
	// target executes the binary.
	ExpectedBinarySHA256 string
	Now                  func() time.Time
}

type VerifyOptions struct {
	ExpectedCLIRevision          string
	ExpectedCatalogRevision      string
	ExpectedPlanDigest           string
	ExpectedTargetID             string
	ExpectedBinarySHA256         string
	RequirePublicationAcceptable bool
	Now                          time.Time
	MaxAge                       time.Duration
}
