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
