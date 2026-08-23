package evidence

import (
	"encoding/json"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/capability"
	"github.com/zzanghyunmoo/my-desk-setup/internal/state"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
)

const (
	SchemaVersion           = "mds.target-evidence/v2"
	CaptureKindActualTarget = "actual-target"
	PreparationSchema       = "mds.certification-preparation/v1"

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
	SchemaVersion   string              `json:"schema_version"`
	CaptureKind     string              `json:"capture_kind"`
	Status          Status              `json:"status"`
	Cohort          string              `json:"cohort"`
	CapturedAtUnix  json.Number         `json:"captured_at_unix"`
	Target          TargetIdentity      `json:"target"`
	CLI             CLIIdentity         `json:"cli"`
	BinarySHA256    string              `json:"binary_sha256"`
	CatalogRevision string              `json:"catalog_revision"`
	PlanDigest      string              `json:"plan_digest"`
	Components      []ComponentCheck    `json:"components"`
	ApplyReceipt    *state.Receipt      `json:"apply_receipt,omitempty"`
	RepeatReceipt   *state.Receipt      `json:"repeat_receipt,omitempty"`
	Capabilities    *capability.Receipt `json:"capabilities,omitempty"`
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

type Preparation struct {
	SchemaVersion                string         `json:"schema_version"`
	Target                       TargetIdentity `json:"target"`
	CLI                          CLIIdentity    `json:"cli"`
	BinarySHA256                 string         `json:"binary_sha256"`
	CatalogRevision              string         `json:"catalog_revision"`
	PlanDigest                   string         `json:"plan_digest"`
	GuestCreationNonceCommitment string         `json:"guest_creation_nonce_commitment,omitempty"`
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
	SchemaVersion   string              `json:"schema_version"`
	CatalogRevision string              `json:"catalog_revision"`
	Target          TargetIdentity      `json:"target"`
	CLIRevision     string              `json:"cli_revision"`
	Ready           bool                `json:"ready"`
	Checks          []ComponentCheck    `json:"checks"`
	Capabilities    *capability.Receipt `json:"capabilities,omitempty"`
}

type CertifyRequest struct {
	MDSPath    string
	TargetID   string
	OutputDir  string
	Cohort     string
	All        bool
	Profile    string
	Components []string
	// ExpectedBinarySHA256 binds capture to a release artifact before the
	// target executes the binary.
	ExpectedBinarySHA256 string
	// ExpectedPlanDigest binds capture to an externally reviewed plan before
	// the target executes any mutating action.
	ExpectedPlanDigest string
	// ExpectedGuestCreationNonceCommitment binds a guest marker to the
	// host-reviewed ownership identity without exposing the raw nonce to the
	// runner-wide environment or GitHub metadata.
	ExpectedGuestCreationNonceCommitment string
	Now                                  func() time.Time
	// RuntimeProbe is a deterministic test seam. Production callers leave it
	// nil so certification derives guest identity from protected runtime facts.
	RuntimeProbe func(target.ID) (target.Facts, error)
}

type PrepareRequest struct {
	MDSPath    string
	TargetID   string
	All        bool
	Profile    string
	Components []string

	ExpectedBinarySHA256 string

	// RuntimeProbe is a deterministic test seam shared with Certify.
	RuntimeProbe func(target.ID) (target.Facts, error)
}

type VerifyOptions struct {
	ExpectedCLIRevision     string
	ExpectedCatalogRevision string
	ExpectedPlanDigest      string
	ExpectedTargetID        string
	ExpectedBinarySHA256    string
	ExpectedCohort          string
	RequireVerified         bool
	Now                     time.Time
	MaxAge                  time.Duration
}
