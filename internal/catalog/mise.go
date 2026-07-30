package catalog

import (
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

func validateMiseFiles(environment Environment) []string {
	var managed []Component
	for _, component := range environment.Catalog.Components {
		if len(misePackages(component)) > 0 {
			managed = append(managed, component)
		}
	}
	if len(managed) == 0 {
		return nil
	}
	var configDocument map[string]any
	if err := toml.Unmarshal([]byte(environment.Mise.Config), &configDocument); err != nil {
		return []string{fmt.Sprintf("mise.toml is invalid: %v", err)}
	}
	var lockDocument map[string]any
	if err := toml.Unmarshal([]byte(environment.Mise.Lock), &lockDocument); err != nil {
		return []string{fmt.Sprintf("mise.lock is invalid: %v", err)}
	}
	configTools, ok := stringMap(configDocument["tools"])
	if !ok {
		return []string{"mise.toml requires a tools table"}
	}
	lockTools, ok := stringMap(lockDocument["tools"])
	if !ok {
		return []string{"mise.lock requires a tools table"}
	}

	var problems []string
	for _, component := range managed {
		packages := misePackages(component)
		if len(packages) == 0 {
			continue
		}
		if len(packages) != 1 {
			problems = append(
				problems,
				fmt.Sprintf("component %q must use one mise package across targets", component.ID),
			)
			continue
		}
		entry := environment.Lock.Versions[component.VersionPolicy.LockKey]
		problems = append(
			problems,
			validateMiseTool(packages[0], entry, configTools, lockTools)...,
		)
	}
	return problems
}

func validateMiseTool(
	packageName string,
	entry LockEntry,
	configTools,
	lockTools map[string]any,
) []string {
	expectedRef := entry.InstallRef
	if expectedRef == "" {
		expectedRef = entry.Version
	}
	configTool, exists := configTools[packageName]
	if !exists {
		return []string{fmt.Sprintf("mise.toml is missing tool %q", packageName)}
	}
	var platformSource map[string]any
	switch value := configTool.(type) {
	case string:
		if value != expectedRef {
			return []string{
				fmt.Sprintf(
					"mise.toml tool %q ref %q does not match versions lock ref %q",
					packageName,
					value,
					expectedRef,
				),
			}
		}
	case map[string]any:
		version, _ := value["version"].(string)
		if version != entry.Version {
			return []string{
				fmt.Sprintf(
					"mise.toml tool %q version %q does not match versions lock version %q",
					packageName,
					version,
					entry.Version,
				),
			}
		}
		platformSource, _ = stringMap(value["platforms"])
	default:
		return []string{fmt.Sprintf("mise.toml tool %q has an invalid declaration", packageName)}
	}

	lockEntries, ok := anySlice(lockTools[packageName])
	if !ok || len(lockEntries) != 1 {
		return []string{fmt.Sprintf("mise.lock requires one tool entry for %q", packageName)}
	}
	lockTool, ok := stringMap(lockEntries[0])
	if !ok {
		return []string{fmt.Sprintf("mise.lock tool %q has an invalid entry", packageName)}
	}
	lockVersion, _ := lockTool["version"].(string)
	if strings.HasPrefix(packageName, "http:") {
		if lockVersion != entry.Version {
			return []string{
				fmt.Sprintf(
					"mise.lock tool %q version %q does not match versions lock version %q",
					packageName,
					lockVersion,
					entry.Version,
				),
			}
		}
	} else if lockVersion != expectedRef {
		return []string{
			fmt.Sprintf(
				"mise.lock tool %q ref %q does not match versions lock ref %q",
				packageName,
				lockVersion,
				expectedRef,
			),
		}
	}
	if platformSource == nil {
		platformSource = make(map[string]any, 2)
		for _, platform := range []string{"linux-x64", "linux-arm64"} {
			if value, exists := lockTool["platforms."+platform]; exists {
				platformSource[platform] = value
			}
		}
	}

	var problems []string
	for catalogPlatform, misePlatform := range map[string]string{
		"linux-amd64": "linux-x64",
		"linux-arm64": "linux-arm64",
	} {
		artifact, available := entry.Artifacts[catalogPlatform]
		_, unavailable := entry.UnavailablePlatforms[catalogPlatform]
		platform, locked := stringMap(platformSource[misePlatform])
		switch {
		case available && !locked:
			problems = append(
				problems,
				fmt.Sprintf(
					"mise lock tool %q is missing reviewed platform %q",
					packageName,
					misePlatform,
				),
			)
		case unavailable && locked:
			problems = append(
				problems,
				fmt.Sprintf(
					"mise lock tool %q unexpectedly declares unavailable platform %q",
					packageName,
					misePlatform,
				),
			)
		case available:
			url, _ := platform["url"].(string)
			checksum, _ := platform["checksum"].(string)
			if url != artifact.URL || checksum != "sha256:"+artifact.SHA256 {
				problems = append(
					problems,
					fmt.Sprintf(
						"mise lock tool %q platform %q does not match versions lock artifact",
						packageName,
						misePlatform,
					),
				)
			}
		}
	}
	return problems
}

func misePackages(component Component) []string {
	seen := map[string]bool{}
	var result []string
	for _, targetKind := range TargetKinds {
		support := component.Targets[targetKind]
		if support.Status != StatusSupported ||
			support.Installer != "mise" ||
			seen[support.Package] {
			continue
		}
		seen[support.Package] = true
		result = append(result, support.Package)
	}
	return result
}

func stringMap(value any) (map[string]any, bool) {
	result, ok := value.(map[string]any)
	return result, ok
}

func anySlice(value any) ([]any, bool) {
	switch values := value.(type) {
	case []any:
		return values, true
	case []map[string]any:
		result := make([]any, len(values))
		for index := range values {
			result[index] = values[index]
		}
		return result, true
	default:
		return nil, false
	}
}
