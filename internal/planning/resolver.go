package planning

import (
	"fmt"
	"sort"
	"strings"

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
		if selection.allowDependencyOnly {
			if len(selection.Components) != 1 {
				return Plan{}, fmt.Errorf(
					"update selection requires exactly one component",
				)
			}
			resolved, err = catalog.ResolveComponentForUpdate(
				environment,
				selection.Components[0],
				targetKind,
			)
		} else {
			resolved, err = catalog.ResolveSelection(
				environment,
				selection.Components,
				targetKind,
			)
		}
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
	resolvedIDs := make([]string, 0, len(resolved))
	for _, item := range resolved {
		resolvedIDs = append(resolvedIDs, item.Component.ID)
	}
	editorSlices := strings.Join(EditorSlices(resolvedIDs), ",")
	runtimeInputs := runtimeTreeInputs(environment, resolved, facts)
	for _, item := range resolved {
		action := actionFor(environment, facts, item)
		if bindsEditorSlices(action.ComponentID) {
			if action.Inputs == nil {
				action.Inputs = make(map[string]string)
			}
			action.Inputs[EditorSlicesInput] = editorSlices
			for key, value := range runtimeInputs {
				action.Inputs[key] = value
			}
		}
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
	facts target.Facts,
	item catalog.ResolvedComponent,
) Action {
	component := item.Component
	targetID := facts.ID
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
	if component.ID == "lima" || component.ID == "wsl" {
		if specification, exists := environment.Targets["ubuntu-26.04"]; exists {
			image := specification.Images[facts.Architecture]
			imageKind := "lima"
			if component.ID == "wsl" {
				image = specification.WSLImages[facts.Architecture]
				imageKind = "wsl"
			}
			action.Inputs = map[string]string{
				"guest_distribution": specification.WSLDistribution,
				"image_kind":         imageKind,
				"image_sha256":       image.SHA256,
				"image_url":          image.URL,
			}
		}
	}
	if item.Support.Installer == "mise" &&
		component.VersionPolicy.Mode == "pinned" {
		lock := environment.Lock.Versions[component.VersionPolicy.LockKey]
		platform := "linux-" + facts.Architecture
		artifact, available := lock.Artifacts[platform]
		if !available {
			reason := lock.UnavailablePlatforms[platform]
			if reason == "" {
				reason = "the reviewed mise lock has no artifact identity for this platform"
			}
			action.Status = ActionActionRequired
			action.Reason = fmt.Sprintf("%s (%s)", reason, platform)
		} else {
			if action.Inputs == nil {
				action.Inputs = make(map[string]string)
			}
			installRef := lock.InstallRef
			if installRef == "" {
				installRef = lock.Version
			}
			action.Inputs["artifact_sha256"] = artifact.SHA256
			action.Inputs["artifact_url"] = artifact.URL
			action.Inputs["mise_ref"] = installRef
		}
	}

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
