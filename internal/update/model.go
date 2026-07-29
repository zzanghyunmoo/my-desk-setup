package update

import (
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/state"
)

const (
	PlanSchema   = "mds.update/v1"
	ResultSchema = "mds.update-result/v1"
)

type Candidate struct {
	ComponentID string                      `json:"component_id"`
	Version     string                      `json:"version"`
	Source      string                      `json:"source"`
	Provenance  string                      `json:"provenance"`
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
	Digest                string            `json:"digest"`
}

type Result struct {
	SchemaVersion   string        `json:"schema_version"`
	UpdateDigest    string        `json:"update_digest"`
	CatalogRevision string        `json:"catalog_revision"`
	Receipt         state.Receipt `json:"receipt"`
}
