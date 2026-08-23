package planning

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestEditorSlicesAreNormalizedAndIndependent(t *testing.T) {
	for _, test := range []struct {
		selection []string
		want      []string
	}{
		{[]string{"nvchad"}, []string{"core"}},
		{[]string{"nvim-ide-tools"}, []string{"legacy"}},
		{[]string{"nvim-jvm", "nvim-dotnet", "nvim-jvm"}, []string{"dotnet", "jvm"}},
		{[]string{"nvim-jvm", "nvim-dotnet", "nvim-ide-tools"}, []string{"dotnet", "jvm", "legacy"}},
	} {
		if got := EditorSlices(test.selection); !reflect.DeepEqual(got, test.want) {
			t.Fatalf("EditorSlices(%v) = %v, want %v", test.selection, got, test.want)
		}
	}
}

func TestRuntimeTreePlanIdentityIsCanonicalJSON(t *testing.T) {
	identity := RuntimeTreeReference{
		ComponentID: "jdt-language-server", ArchiveSHA256: "archive",
		ManifestSHA256: "manifest", Executable: "bin/jdtls", LauncherSHA256: "launcher",
		RequiredPaths: []string{"z", "a"},
	}
	encoded, err := json.Marshal(identity)
	if err != nil || string(encoded) == "" {
		t.Fatalf("Marshal(identity) = %s, %v", encoded, err)
	}
}
