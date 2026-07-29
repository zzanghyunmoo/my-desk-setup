package planning

import (
	"fmt"
	"sort"

	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
)

func Build(
	environment catalog.Environment,
	facts target.Facts,
	selection Selection,
) (Plan, error) {
	targetKind := catalog.TargetKind(facts.ID.Kind)
	var (
		resolved []catalog.ResolvedComponent
		err      error
	)
	switch selection.Mode {
	case SelectionAll:
		resolved, err = catalog.ResolveProfile(environment, "all", targetKind)
	case SelectionProfile:
		resolved, err = catalog.ResolveProfile(environment, selection.Profile, targetKind)
	case SelectionComponents:
		resolved, err = catalog.ResolveSelection(environment, selection.Components, targetKind)
	default:
		return Plan{}, fmt.Errorf("invalid selection mode %q", selection.Mode)
	}
	if err != nil {
		return Plan{}, err
	}

	revision, err := catalog.Revision(environment)
	if err != nil {
		return Plan{}, fmt.Errorf("catalog revision: %w", err)
	}
	plan := Plan{
		SchemaVersion:   PlanSchema,
		CatalogRevision: revision,
		Target:          facts,
	}
	for _, item := range resolved {
		action := actionFor(environment, facts.ID, item)
		plan.Selection = append(plan.Selection, item.Component.ID)
		plan.Actions = append(plan.Actions, action)
		if action.Status != ActionPlanned {
			plan.Blockers = append(plan.Blockers, Blocker{
				ActionID: action.ID,
				Status:   action.Status,
				Reason:   action.Reason,
			})
		}
	}
	sort.Strings(plan.Selection)
	sort.Slice(plan.Blockers, func(left, right int) bool {
		return plan.Blockers[left].ActionID < plan.Blockers[right].ActionID
	})

	plan.Digest, err = Digest(plan)
	if err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func actionFor(
	environment catalog.Environment,
	targetID target.ID,
	item catalog.ResolvedComponent,
) Action {
	component := item.Component
	action := Action{
		ID:           targetID.String() + "/" + component.ID,
		ComponentID:  component.ID,
		TargetID:     targetID.String(),
		Status:       ActionPlanned,
		Installer:    item.Support.Installer,
		Package:      item.Support.Package,
		Version:      resolvedVersion(environment, component),
		Dependencies: make([]string, 0, len(component.Dependencies)),
		Verification: [][]string{
			append([]string(nil), component.Verification.Command...),
		},
	}
	if len(component.Verification.Functional) > 0 {
		action.Verification = append(
			action.Verification,
			append([]string(nil), component.Verification.Functional...),
		)
	}
	for _, dependency := range component.Dependencies {
		action.Dependencies = append(
			action.Dependencies,
			targetID.String()+"/"+dependency,
		)
	}
	sort.Strings(action.Dependencies)

	switch item.Support.Status {
	case catalog.StatusUnsupported:
		action.Status = ActionUnsupported
		action.Reason = item.Support.Reason
	case catalog.StatusActionRequired:
		action.Status = ActionActionRequired
		action.Reason = item.Support.Reason
	}
	return action
}

func resolvedVersion(environment catalog.Environment, component catalog.Component) string {
	switch component.VersionPolicy.Mode {
	case "pinned":
		return environment.Lock.Versions[component.VersionPolicy.LockKey].Version
	case "manager":
		return "manager-owned"
	case "manual":
		return "manual"
	default:
		return "unknown"
	}
}
