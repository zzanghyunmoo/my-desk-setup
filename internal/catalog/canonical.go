package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

func CanonicalJSON(environment Environment) ([]byte, error) {
	normalized := normalize(environment)
	return json.Marshal(normalized)
}

func Revision(environment Environment) (string, error) {
	canonical, err := CanonicalJSON(environment)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func normalize(environment Environment) Environment {
	normalized := environment
	normalized.Catalog.Components = append([]Component(nil), environment.Catalog.Components...)
	sort.Slice(normalized.Catalog.Components, func(left, right int) bool {
		return normalized.Catalog.Components[left].ID < normalized.Catalog.Components[right].ID
	})
	for index := range normalized.Catalog.Components {
		component := &normalized.Catalog.Components[index]
		component.Provides = sorted(component.Provides)
		component.Dependencies = sorted(component.Dependencies)
		component.Verification.Command = append([]string(nil), component.Verification.Command...)
		component.Verification.Functional = append([]string(nil), component.Verification.Functional...)
	}
	if environment.Lock.CompatibilityEpochs != nil {
		normalized.Lock.CompatibilityEpochs = make(
			map[string]CompatibilityEpoch,
			len(environment.Lock.CompatibilityEpochs),
		)
		for id, epoch := range environment.Lock.CompatibilityEpochs {
			epoch.Members = sorted(epoch.Members)
			normalized.Lock.CompatibilityEpochs[id] = epoch
		}
	}
	normalized.Lock.Versions = make(map[string]LockEntry, len(environment.Lock.Versions))
	for key, entry := range environment.Lock.Versions {
		if entry.Artifacts != nil {
			entry.Artifacts = make(map[string]Artifact, len(entry.Artifacts))
			for platform, artifact := range environment.Lock.Versions[key].Artifacts {
				if artifact.Tree != nil {
					tree := *artifact.Tree
					tree.RequiredPaths = sorted(tree.RequiredPaths)
					artifact.Tree = &tree
				}
				entry.Artifacts[platform] = artifact
			}
		}
		normalized.Lock.Versions[key] = entry
	}

	normalized.Profiles = make(map[string]Profile, len(environment.Profiles))
	for id, profile := range environment.Profiles {
		profile.Selection = sorted(profile.Selection)
		normalized.Profiles[id] = profile
	}
	return normalized
}

func sorted(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}
