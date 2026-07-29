package catalog

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

var credentialValuePattern = regexp.MustCompile(
	`(?i)(api[_-]?key|bearer|credential|password|secret|token)\s*[:=]\s*\S+`,
)

func Validate(environment Environment) error {
	var problems []string
	if environment.Catalog.SchemaVersion != 1 {
		problems = append(problems, "catalog schema_version must be 1")
	}
	if environment.Lock.SchemaVersion != 1 {
		problems = append(problems, "lock schema_version must be 1")
	}

	components := make(map[string]Component, len(environment.Catalog.Components))
	capabilityOwner := make(map[string]string)
	lockKeys := make(map[string]string)
	for _, component := range environment.Catalog.Components {
		if component.ID == "" {
			problems = append(problems, "component id is required")
			continue
		}
		if _, exists := components[component.ID]; exists {
			problems = append(problems, fmt.Sprintf("duplicate component id %q", component.ID))
			continue
		}
		components[component.ID] = component
		if component.Name == "" || component.Kind == "" {
			problems = append(problems, fmt.Sprintf("component %q requires name and kind", component.ID))
		}
		if len(component.Provides) == 0 {
			problems = append(problems, fmt.Sprintf("component %q provides no capabilities", component.ID))
		}
		problems = append(
			problems,
			duplicateValues("component "+component.ID+" provides", component.Provides)...,
		)
		problems = append(
			problems,
			duplicateValues("component "+component.ID+" dependencies", component.Dependencies)...,
		)
		for _, capability := range component.Provides {
			if owner, exists := capabilityOwner[capability]; exists {
				problems = append(
					problems,
					fmt.Sprintf("capability %q has duplicate owners %q and %q", capability, owner, component.ID),
				)
				continue
			}
			capabilityOwner[capability] = component.ID
		}
		problems = append(problems, validateTargets(component)...)
		problems = append(problems, validateVersionPolicy(component, environment.Lock, lockKeys)...)
	}

	for _, component := range environment.Catalog.Components {
		for _, dependency := range component.Dependencies {
			dependencyComponent, exists := components[dependency]
			if !exists {
				problems = append(
					problems,
					fmt.Sprintf("component %q references unknown dependency %q", component.ID, dependency),
				)
				continue
			}
			for _, target := range TargetKinds {
				if component.Targets[target].Status == StatusUnsupported {
					continue
				}
				if dependencyComponent.Targets[target].Status == StatusUnsupported {
					problems = append(
						problems,
						fmt.Sprintf(
							"component %q target %q depends on unsupported component %q",
							component.ID,
							target,
							dependency,
						),
					)
				}
			}
		}
	}
	problems = append(problems, detectCycles(components)...)

	for id, profile := range environment.Profiles {
		if profile.SchemaVersion != 1 {
			problems = append(problems, fmt.Sprintf("profile %q schema_version must be 1", id))
		}
		if id != profile.ID {
			problems = append(problems, fmt.Sprintf("profile map key %q does not match id %q", id, profile.ID))
		}
		if profile.ID == "all" {
			problems = append(problems, `profile id "all" is reserved for computed selection`)
		}
		problems = append(
			problems,
			duplicateValues("profile "+profile.ID+" selection", profile.Selection)...,
		)
		for _, selection := range profile.Selection {
			if _, component := components[selection]; component {
				continue
			}
			if _, capability := capabilityOwner[selection]; !capability {
				problems = append(
					problems,
					fmt.Sprintf("profile %q references unknown selection %q", id, selection),
				)
			}
		}
	}

	for key := range environment.Lock.Versions {
		if _, used := lockKeys[key]; !used {
			problems = append(problems, fmt.Sprintf("lock contains unused key %q", key))
		}
	}
	problems = append(problems, findCredentialMaterial(reflect.ValueOf(environment), "environment")...)

	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return errors.New(strings.Join(problems, "; "))
}

func duplicateValues(scope string, values []string) []string {
	seen := make(map[string]bool, len(values))
	var problems []string
	for _, value := range values {
		if seen[value] {
			problems = append(problems, fmt.Sprintf("%s contains duplicate %q", scope, value))
			continue
		}
		seen[value] = true
	}
	return problems
}

func validateTargets(component Component) []string {
	var problems []string
	for _, target := range TargetKinds {
		support, exists := component.Targets[target]
		if !exists {
			problems = append(
				problems,
				fmt.Sprintf("component %q does not declare target %q", component.ID, target),
			)
			continue
		}
		switch support.Status {
		case StatusSupported:
			if support.Installer == "" || support.Package == "" {
				problems = append(
					problems,
					fmt.Sprintf("component %q target %q supported cell requires installer and package", component.ID, target),
				)
			}
			if !installerAllowed(target, support.Installer) {
				problems = append(
					problems,
					fmt.Sprintf("component %q target %q cannot use installer %q", component.ID, target, support.Installer),
				)
			}
		case StatusUnsupported, StatusActionRequired:
			if support.Reason == "" {
				problems = append(
					problems,
					fmt.Sprintf("component %q target %q status %q requires a reason", component.ID, target, support.Status),
				)
			}
		default:
			problems = append(
				problems,
				fmt.Sprintf("component %q target %q has invalid status %q", component.ID, target, support.Status),
			)
		}
	}
	for target := range component.Targets {
		if !knownTarget(target) {
			problems = append(
				problems,
				fmt.Sprintf("component %q declares unknown target %q", component.ID, target),
			)
		}
	}
	return problems
}

func validateVersionPolicy(
	component Component,
	lock VersionLock,
	lockKeys map[string]string,
) []string {
	switch component.VersionPolicy.Mode {
	case "pinned":
		key := component.VersionPolicy.LockKey
		if key == "" {
			return []string{fmt.Sprintf("component %q pinned policy requires lock_key", component.ID)}
		}
		if owner, exists := lockKeys[key]; exists {
			return []string{
				fmt.Sprintf("lock key %q is shared by components %q and %q", key, owner, component.ID),
			}
		}
		lockKeys[key] = component.ID
		entry, exists := lock.Versions[key]
		if !exists {
			return []string{fmt.Sprintf("component %q references missing lock key %q", component.ID, key)}
		}
		if entry.Version == "" || entry.Source == "" || entry.Provenance == "" {
			return []string{
				fmt.Sprintf("lock key %q requires version, source, and provenance", key),
			}
		}
		for platform, artifact := range entry.Artifacts {
			if artifact.URL == "" || len(artifact.SHA256) != 64 ||
				(artifact.Format != "binary" &&
					artifact.Format != "zip" &&
					artifact.Format != "tar.gz") ||
				artifact.Executable == "" {
				return []string{
					fmt.Sprintf(
						"lock key %q artifact %q requires URL, SHA-256, binary/zip/tar.gz format, and executable",
						key,
						platform,
					),
				}
			}
		}
	case "manager", "manual":
		if component.VersionPolicy.LockKey != "" {
			return []string{
				fmt.Sprintf("component %q mode %q cannot declare lock_key", component.ID, component.VersionPolicy.Mode),
			}
		}
	default:
		return []string{
			fmt.Sprintf("component %q has invalid version policy %q", component.ID, component.VersionPolicy.Mode),
		}
	}
	return nil
}

func installerAllowed(target TargetKind, installer string) bool {
	allowed := map[TargetKind]map[string]bool{
		TargetMacOSHost: {
			"brew": true, "bun": true, "mise": true, "script": true, "vendor": true, "manual": true, "system": true,
		},
		TargetWindowsHost: {
			"winget": true, "bun": true, "mise": true, "script": true, "vendor": true, "manual": true, "system": true,
		},
		TargetWSLGuest: {
			"apt": true, "bun": true, "mise": true, "script": true, "vendor": true, "manual": true, "docker-apt": true, "system": true,
		},
		TargetLimaGuest: {
			"apt": true, "bun": true, "mise": true, "script": true, "vendor": true, "manual": true, "docker-apt": true, "system": true,
		},
	}
	return allowed[target][installer]
}

func knownTarget(target TargetKind) bool {
	for _, candidate := range TargetKinds {
		if target == candidate {
			return true
		}
	}
	return false
}

func detectCycles(components map[string]Component) []string {
	const (
		unseen = iota
		visiting
		visited
	)
	state := make(map[string]int, len(components))
	var stack []string
	var problems []string

	var visit func(string)
	visit = func(id string) {
		switch state[id] {
		case visiting:
			start := 0
			for index, item := range stack {
				if item == id {
					start = index
					break
				}
			}
			cycle := append(append([]string{}, stack[start:]...), id)
			problems = append(problems, "dependency cycle: "+strings.Join(cycle, " -> "))
			return
		case visited:
			return
		}
		state[id] = visiting
		stack = append(stack, id)
		for _, dependency := range components[id].Dependencies {
			if _, exists := components[dependency]; exists {
				visit(dependency)
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = visited
	}

	ids := make([]string, 0, len(components))
	for id := range components {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		visit(id)
	}
	return problems
}

func findCredentialMaterial(value reflect.Value, path string) []string {
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		return findCredentialMaterial(value.Elem(), path)
	}

	var problems []string
	switch value.Kind() {
	case reflect.Struct:
		valueType := value.Type()
		for index := 0; index < value.NumField(); index++ {
			field := valueType.Field(index)
			problems = append(
				problems,
				findCredentialMaterial(value.Field(index), path+"."+field.Name)...,
			)
		}
	case reflect.Map:
		iterator := value.MapRange()
		for iterator.Next() {
			key := fmt.Sprint(iterator.Key().Interface())
			problems = append(
				problems,
				findCredentialMaterial(iterator.Value(), path+"."+key)...,
			)
		}
	case reflect.Slice, reflect.Array:
		for index := 0; index < value.Len(); index++ {
			problems = append(
				problems,
				findCredentialMaterial(value.Index(index), fmt.Sprintf("%s[%d]", path, index))...,
			)
		}
	case reflect.String:
		if credentialValuePattern.MatchString(value.String()) {
			problems = append(problems, fmt.Sprintf("%s contains credential-like material", path))
		}
	}
	return problems
}
