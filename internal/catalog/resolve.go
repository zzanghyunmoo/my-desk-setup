package catalog

import (
	"fmt"
	"sort"
)

func ResolveProfile(
	environment Environment,
	profileID string,
	target TargetKind,
) ([]ResolvedComponent, error) {
	if !knownTarget(target) {
		return nil, fmt.Errorf("unknown target %q", target)
	}
	if profileID == "all" {
		return resolveAll(environment, target), nil
	}
	profile, exists := environment.Profiles[profileID]
	if !exists {
		return nil, fmt.Errorf("unknown profile %q", profileID)
	}
	return ResolveSelection(environment, profile.Selection, target)
}

func ResolveSelection(
	environment Environment,
	selection []string,
	target TargetKind,
) ([]ResolvedComponent, error) {
	if !knownTarget(target) {
		return nil, fmt.Errorf("unknown target %q", target)
	}
	components, capabilities := index(environment)
	for _, reference := range selection {
		id := reference
		if owner, exists := capabilities[reference]; exists {
			id = owner
		}
		component, exists := components[id]
		if !exists {
			continue
		}
		if component.SelectionPolicy == SelectionPolicyDependencyOnly {
			return nil, fmt.Errorf(
				"component %q is dependency-only and cannot be selected directly",
				component.ID,
			)
		}
	}
	return resolveSelection(components, capabilities, selection, target)
}

// ResolveComponentForUpdate resolves a reviewed, exact component replacement.
// Unlike normal user selection, this permits a dependency-only component to be
// the root while preserving the same dependency closure and target checks.
func ResolveComponentForUpdate(
	environment Environment,
	componentID string,
	target TargetKind,
) ([]ResolvedComponent, error) {
	if !knownTarget(target) {
		return nil, fmt.Errorf("unknown target %q", target)
	}
	components, capabilities := index(environment)
	if _, exists := components[componentID]; !exists {
		return nil, fmt.Errorf("unknown selection %q", componentID)
	}
	return resolveSelection(
		components,
		capabilities,
		[]string{componentID},
		target,
	)
}

// SelectionCandidates returns the stable user-facing root set before a target
// is observed. Dependencies remain resolvable but are never offered directly.
func SelectionCandidates(environment Environment) []Component {
	result := make([]Component, 0, len(environment.Catalog.Components))
	for _, component := range environment.Catalog.Components {
		if component.SelectionPolicy == SelectionPolicyDependencyOnly {
			continue
		}
		result = append(result, component)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].ID < result[right].ID
	})
	return result
}

// SelectionRoots returns the target-reachable direct root set used by --all.
// A dependency-only component can still appear in the resulting closure.
func SelectionRoots(environment Environment, target TargetKind) []Component {
	result := make([]Component, 0, len(environment.Catalog.Components))
	for _, component := range SelectionCandidates(environment) {
		if component.Targets[target].Status == StatusUnsupported {
			continue
		}
		result = append(result, component)
	}
	return result
}

func resolveSelection(
	components map[string]Component,
	capabilities map[string]string,
	selection []string,
	target TargetKind,
) ([]ResolvedComponent, error) {
	selected := make(map[string]bool)
	visiting := make(map[string]bool)
	var ordered []string

	var add func(string) error
	add = func(reference string) error {
		id := reference
		if owner, exists := capabilities[reference]; exists {
			id = owner
		}
		component, exists := components[id]
		if !exists {
			return fmt.Errorf("unknown selection %q", reference)
		}
		if selected[id] {
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("dependency cycle while resolving %q", id)
		}
		visiting[id] = true
		dependencies := append([]string(nil), component.Dependencies...)
		sort.Strings(dependencies)
		for _, dependency := range dependencies {
			if err := add(dependency); err != nil {
				return err
			}
		}
		delete(visiting, id)
		selected[id] = true
		ordered = append(ordered, id)
		return nil
	}

	references := append([]string(nil), selection...)
	sort.Strings(references)
	for _, reference := range references {
		if err := add(reference); err != nil {
			return nil, err
		}
	}
	return materialize(ordered, components, target), nil
}

func resolveAll(environment Environment, target TargetKind) []ResolvedComponent {
	components, capabilities := index(environment)
	ids := make([]string, 0, len(components))
	for _, component := range SelectionRoots(environment, target) {
		ids = append(ids, component.ID)
	}
	resolved, err := resolveSelection(components, capabilities, ids, target)
	if err != nil {
		return nil
	}
	return resolved
}

func materialize(
	ids []string,
	components map[string]Component,
	target TargetKind,
) []ResolvedComponent {
	result := make([]ResolvedComponent, 0, len(ids))
	for _, id := range ids {
		component := components[id]
		result = append(result, ResolvedComponent{
			Component: component,
			Target:    target,
			Support:   component.Targets[target],
		})
	}
	return result
}

func index(environment Environment) (map[string]Component, map[string]string) {
	components := make(map[string]Component, len(environment.Catalog.Components))
	capabilities := make(map[string]string)
	for _, component := range environment.Catalog.Components {
		components[component.ID] = component
		for _, capability := range component.Provides {
			capabilities[capability] = component.ID
		}
	}
	return components, capabilities
}
