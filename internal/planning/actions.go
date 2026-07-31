package planning

import "github.com/zzanghyunmoo/my-desk-setup/internal/target"

const PlanSchema = "mds.plan/v2"

type ActionStatus string

const (
	ActionPlanned        ActionStatus = "planned"
	ActionUnsupported    ActionStatus = "unsupported"
	ActionActionRequired ActionStatus = "action-required"
)

type Plan struct {
	SchemaVersion   string       `json:"schema_version"`
	CatalogRevision string       `json:"catalog_revision"`
	Target          target.Facts `json:"target"`
	Selection       []string     `json:"selection"`
	Actions         []Action     `json:"actions"`
	Blockers        []Blocker    `json:"blockers"`
	Digest          string       `json:"digest"`
}

type Action struct {
	ID           string            `json:"id"`
	ComponentID  string            `json:"component_id"`
	TargetID     string            `json:"target_id"`
	Status       ActionStatus      `json:"status"`
	Installer    string            `json:"installer,omitempty"`
	Package      string            `json:"package,omitempty"`
	Version      string            `json:"version"`
	Dependencies []string          `json:"dependencies"`
	Verification [][]string        `json:"verification"`
	Inputs       map[string]string `json:"inputs,omitempty"`
	Reason       string            `json:"reason,omitempty"`
}

type Blocker struct {
	ActionID string       `json:"action_id"`
	Status   ActionStatus `json:"status"`
	Reason   string       `json:"reason"`
}
