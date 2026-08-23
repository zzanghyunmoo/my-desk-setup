package adapters

import (
	"context"
	"fmt"

	"github.com/zzanghyunmoo/my-desk-setup/internal/capability"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

type CapabilityProber interface {
	ProbeCapabilities(context.Context, planning.Plan) capability.Receipt
}

type ActionRequiredError struct {
	Reason string
}

func (err *ActionRequiredError) Error() string {
	return fmt.Sprintf("action-required: %s", err.Reason)
}

type ObservedState string

const (
	StateAbsent   ObservedState = "absent"
	StateReady    ObservedState = "ready"
	StateConflict ObservedState = "conflict"
)

type Observation struct {
	State            ObservedState `json:"state"`
	InstalledVersion string        `json:"installed_version,omitempty"`
	Detail           string        `json:"detail,omitempty"`
}

type Component interface {
	Observe(context.Context, planning.Action) (Observation, error)
	Apply(context.Context, planning.Action) error
	Verify(context.Context, planning.Action) error
}

// PlanPreflighter performs component-specific, read-only validation before
// any action in an approved plan is allowed to mutate the target. The cleanup
// keeps verified temporary inputs alive for the duration of the apply.
type PlanPreflighter interface {
	Preflight(context.Context, planning.Plan) (func() error, error)
}
