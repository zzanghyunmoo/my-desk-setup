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
	return resolveSelection(components, capabilities, selection, target)
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
	for id, component := range components {
		if component.Targets[target].Status != StatusUnsupported {
			ids = append(ids, id)
		}
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
