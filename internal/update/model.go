package update

import (
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/state"
)

const (
	PlanSchema   = "mds.update/v2"
	ResultSchema = "mds.update-result/v1"
)

type Candidate struct {
	ComponentID string                      `json:"component_id"`
	Version     string                      `json:"version"`
	Source      string                      `json:"source"`
	Provenance  string                      `json:"provenance"`
	NPM         *catalog.NPMArtifact        `json:"npm,omitempty"`
	Artifacts   map[string]catalog.Artifact `json:"artifacts,omitempty"`
}

type Plan struct {
	SchemaVersion         string            `json:"schema_version"`
	ComponentID           string            `json:"component_id"`
	LockKey               string            `json:"lock_key"`
	BeforeCatalogRevision string            `json:"before_catalog_revision"`
	AfterCatalogRevision  string            `json:"after_catalog_revision"`
	Old                   catalog.LockEntry `json:"old"`
	New                   catalog.LockEntry `json:"new"`
	TargetPlan            planning.Plan     `json:"target_plan"`
	CompatibilityMatrix   []MatrixEntry     `json:"compatibility_matrix"`
	Digest                string            `json:"digest"`
}

type MatrixEntry struct {
	TargetKind   catalog.TargetKind `json:"target_kind"`
	TargetID     string             `json:"target_id"`
	Architecture string             `json:"architecture"`
	PlanDigest   string             `json:"plan_digest"`
	ArtifactKey  string             `json:"artifact_key,omitempty"`
}

type Result struct {
	SchemaVersion   string        `json:"schema_version"`
	UpdateDigest    string        `json:"update_digest"`
	CatalogRevision string        `json:"catalog_revision"`
	Receipt         state.Receipt `json:"receipt"`
}
