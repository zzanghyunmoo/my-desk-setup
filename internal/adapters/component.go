package adapters

import (
	"context"
	"fmt"

	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

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
