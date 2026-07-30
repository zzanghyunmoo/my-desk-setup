package catalog

import (
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"

	exactartifact "github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
)

var credentialValuePattern = regexp.MustCompile(
	`(?i)(api[_-]?key|bearer|credential|password|secret|token)\s*[:=]\s*\S+`,
)

var catalogIdentifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

var componentKinds = map[string]bool{
	"agent": true, "build": true, "cli": true, "container": true,
	"editor": true, "gui": true, "language": true, "platform": true,
	"terminal": true,
}

func Validate(environment Environment) error {
	var problems []string
	if environment.Catalog.SchemaVersion != 1 {
		problems = append(problems, "catalog schema_version must be 1")
	}
	if environment.Lock.SchemaVersion != 1 {
		problems = append(problems, "lock schema_version must be 1")
	}
	if len(environment.Catalog.Components) == 0 {
		problems = append(problems, "catalog requires at least one component")
	}
	for id, specification := range environment.Targets {
		problems = append(
			problems,
			validateTargetSpec(id, specification)...,
		)
	}

	components := make(map[string]Component, len(environment.Catalog.Components))
	capabilityOwner := make(map[string]string)
	lockKeys := make(map[string]string)
	for _, component := range environment.Catalog.Components {
		if !catalogIdentifierPattern.MatchString(component.ID) {
			problems = append(
				problems,
				fmt.Sprintf("component id %q must match %s", component.ID, catalogIdentifierPattern),
			)
			continue
		}
		if _, exists := components[component.ID]; exists {
			problems = append(problems, fmt.Sprintf("duplicate component id %q", component.ID))
			continue
		}
		components[component.ID] = component
		if component.Name == "" {
			problems = append(problems, fmt.Sprintf("component %q requires a name", component.ID))
		}
		if !componentKinds[component.Kind] {
			problems = append(
				problems,
				fmt.Sprintf("component %q has invalid kind %q", component.ID, component.Kind),
			)
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
			if !catalogIdentifierPattern.MatchString(capability) {
				problems = append(
					problems,
					fmt.Sprintf("component %q capability %q has an invalid identifier", component.ID, capability),
				)
			}
			if owner, exists := capabilityOwner[capability]; exists {
				problems = append(
					problems,
					fmt.Sprintf("capability %q has duplicate owners %q and %q", capability, owner, component.ID),
				)
				continue
			}
			capabilityOwner[capability] = component.ID
		}
		for _, dependency := range component.Dependencies {
			if !catalogIdentifierPattern.MatchString(dependency) {
				problems = append(
					problems,
					fmt.Sprintf("component %q dependency %q has an invalid identifier", component.ID, dependency),
				)
			}
		}
		if len(component.Verification.Command) == 0 {
			problems = append(
				problems,
				fmt.Sprintf("component %q verification command is required", component.ID),
			)
		}
		for _, argument := range component.Verification.Command {
			if argument == "" {
				problems = append(
					problems,
					fmt.Sprintf("component %q verification command contains an empty argument", component.ID),
				)
			}
		}
		for _, scenario := range component.Verification.Functional {
			if scenario == "" {
				problems = append(
					problems,
					fmt.Sprintf("component %q functional verification contains an empty scenario", component.ID),
				)
			}
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
		if !catalogIdentifierPattern.MatchString(key) {
			problems = append(
				problems,
				fmt.Sprintf("lock key %q has an invalid identifier", key),
			)
		}
		if _, used := lockKeys[key]; !used {
			problems = append(problems, fmt.Sprintf("lock contains unused key %q", key))
		}
	}
	problems = append(problems, validateMiseFiles(environment)...)
	problems = append(problems, findCredentialMaterial(reflect.ValueOf(environment), "environment")...)

	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return errors.New(strings.Join(problems, "; "))
}

func validateTargetSpec(id string, specification TargetSpec) []string {
	var problems []string
	if specification.SchemaVersion != 1 ||
		specification.ID != id ||
		specification.Distribution == "" ||
		specification.Release == "" ||
		specification.WSLDistribution == "" {
		problems = append(
			problems,
			fmt.Sprintf(
				"target %q requires schema_version 1, matching id, distribution, release, and WSL distribution",
				id,
			),
		)
	}
	for label, images := range map[string]map[string]ImageSpec{
		"Lima": specification.Images,
		"WSL":  specification.WSLImages,
	} {
		for _, architecture := range []string{"amd64", "arm64"} {
			image, exists := images[architecture]
			if !exists ||
				validateReviewedHTTPS(image.URL) != nil ||
				exactartifact.ValidateSHA256(image.SHA256) != nil {
				problems = append(
					problems,
					fmt.Sprintf(
						"target %q %s/%s image requires an HTTPS URL and lowercase SHA-256",
						id,
						label,
						architecture,
					),
				)
			}
		}
	}
	return problems
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
		if !catalogIdentifierPattern.MatchString(key) {
			return []string{
				fmt.Sprintf(
					"component %q pinned policy requires a valid lock_key",
					component.ID,
				),
			}
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
		if err := validateReviewedHTTPS(entry.Provenance); err != nil {
			return []string{
				fmt.Sprintf("lock key %q provenance %v", key, err),
			}
		}
		usesMise := componentUsesInstaller(component, "mise")
		if usesMise {
			for _, platform := range []string{"linux-amd64", "linux-arm64"} {
				_, hasArtifact := entry.Artifacts[platform]
				reason, unavailable := entry.UnavailablePlatforms[platform]
				if hasArtifact == unavailable || unavailable && strings.TrimSpace(reason) == "" {
					return []string{
						fmt.Sprintf(
							"lock key %q mise platform %q requires exactly one reviewed artifact or unavailable reason",
							key,
							platform,
						),
					}
				}
			}
		} else if entry.InstallRef != "" || len(entry.UnavailablePlatforms) > 0 {
			return []string{
				fmt.Sprintf(
					"lock key %q cannot declare mise-only install_ref or unavailable_platforms",
					key,
				),
			}
		}
		for platform := range entry.UnavailablePlatforms {
			if !catalogIdentifierPattern.MatchString(platform) {
				return []string{
					fmt.Sprintf(
						"lock key %q unavailable platform %q has an invalid identifier",
						key,
						platform,
					),
				}
			}
		}
		bunPackage, usesBun, packageProblem := componentBunPackage(component)
		if packageProblem != "" {
			return []string{packageProblem}
		}
		switch {
		case usesBun && entry.NPM == nil:
			return []string{
				fmt.Sprintf(
					"lock key %q requires exact npm tarball, SRI, and SHA-256",
					key,
				),
			}
		case usesBun:
			if err := validateNPMArtifact(*entry.NPM, bunPackage, entry.Version); err != nil {
				return []string{
					fmt.Sprintf("lock key %q %v", key, err),
				}
			}
		case entry.NPM != nil:
			return []string{
				fmt.Sprintf(
					"lock key %q cannot declare npm tarball without a Bun installer",
					key,
				),
			}
		}
		for platform, artifact := range entry.Artifacts {
			if !catalogIdentifierPattern.MatchString(platform) ||
				validateReviewedHTTPS(artifact.URL) != nil ||
				exactartifact.ValidateSHA256(artifact.SHA256) != nil ||
				(artifact.Format != "binary" &&
					artifact.Format != "zip" &&
					artifact.Format != "tar.gz" &&
					artifact.Format != "tar.xz") ||
				artifact.Executable == "" {
				return []string{
					fmt.Sprintf(
						"lock key %q artifact %q requires a valid platform identifier, credential-free HTTPS URL, SHA-256, binary/zip/tar.gz/tar.xz format, and executable",
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

func componentBunPackage(component Component) (string, bool, string) {
	var packageName string
	for _, target := range TargetKinds {
		support := component.Targets[target]
		if support.Status != StatusSupported || support.Installer != "bun" {
			continue
		}
		if packageName == "" {
			packageName = support.Package
			continue
		}
		if support.Package != packageName {
			return "", true, fmt.Sprintf(
				"component %q uses inconsistent Bun packages %q and %q",
				component.ID,
				packageName,
				support.Package,
			)
		}
	}
	return packageName, packageName != "", ""
}

func componentUsesInstaller(component Component, installer string) bool {
	for _, target := range TargetKinds {
		support := component.Targets[target]
		if support.Status == StatusSupported && support.Installer == installer {
			return true
		}
	}
	return false
}

func validateReviewedHTTPS(value string) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New(
			"must be an absolute credential-free HTTPS URL without a query or fragment",
		)
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
