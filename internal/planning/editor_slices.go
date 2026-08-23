package planning

import (
	"encoding/json"
	"sort"

	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
)

const EditorSlicesInput = "editor_slices"
const RuntimeTreeInputPrefix = "runtime_tree."

var editorSliceOwner = map[string]string{
	"nvim-ide-tools": "legacy",
	"nvim-jvm":       "jvm",
	"nvim-dotnet":    "dotnet",
}

// EditorSlices is the normalized language slice identity rendered by every
// managed editor action. Core-only remains explicit so a later language
// selection cannot collide with the same action digest.
func EditorSlices(selection []string) []string {
	seen := make(map[string]bool, len(editorSliceOwner))
	for _, componentID := range selection {
		if slice := editorSliceOwner[componentID]; slice != "" {
			seen[slice] = true
		}
	}
	if len(seen) == 0 {
		return []string{"core"}
	}
	result := make([]string, 0, len(seen))
	for slice := range seen {
		result = append(result, slice)
	}
	sort.Strings(result)
	return result
}

func bindsEditorSlices(componentID string) bool {
	if componentID == "nvchad" {
		return true
	}
	_, exists := editorSliceOwner[componentID]
	return exists
}

type RuntimeTreeReference struct {
	ComponentID    string   `json:"component_id"`
	ArchiveSHA256  string   `json:"archive_sha256"`
	ManifestSHA256 string   `json:"manifest_sha256"`
	Executable     string   `json:"executable"`
	LauncherSHA256 string   `json:"launcher_sha256"`
	RequiredPaths  []string `json:"required_paths"`
}

func runtimeTreeInputs(
	environment catalog.Environment,
	resolved []catalog.ResolvedComponent,
	facts target.Facts,
) map[string]string {
	result := make(map[string]string)
	platform := facts.OS + "-" + facts.Architecture
	for _, item := range resolved {
		key := item.Component.VersionPolicy.LockKey
		if key == "" {
			continue
		}
		artifactValue, exists := environment.Lock.Versions[key].Artifacts[platform]
		if !exists || artifactValue.Tree == nil {
			continue
		}
		identity := RuntimeTreeReference{
			ComponentID: item.Component.ID, ArchiveSHA256: artifactValue.SHA256,
			ManifestSHA256: artifactValue.Tree.ManifestSHA256,
			Executable:     artifactValue.Executable, LauncherSHA256: artifactValue.Tree.LauncherSHA256,
			RequiredPaths: append([]string(nil), artifactValue.Tree.RequiredPaths...),
		}
		sort.Strings(identity.RequiredPaths)
		encoded, err := json.Marshal(identity)
		if err != nil {
			continue
		}
		result[RuntimeTreeInputPrefix+item.Component.ID] = string(encoded)
	}
	return result
}
