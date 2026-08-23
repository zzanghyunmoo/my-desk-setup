package guest

import (
	"io/fs"
	"strings"
	"testing"
)

func TestIDEFixtureSetIsCompleteAndBounded(t *testing.T) {
	if err := validateIDEFixtures(); err != nil {
		t.Fatal(err)
	}
	for _, definition := range ideFixtureDefinitions {
		entries := 0
		total := 0
		err := fs.WalkDir(ideFixtures, definition.Root, func(file string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			content, err := ideFixtures.ReadFile(file)
			if err != nil {
				return err
			}
			entries++
			total += len(content)
			if strings.Contains(string(content), "0.0.0.0") || strings.Contains(string(content), "latest") {
				t.Fatalf("fixture %s contains a moving identity or wildcard bind", file)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if entries > 32 || total > 256<<10 {
			t.Fatalf("fixture %s exceeds source bounds: entries=%d bytes=%d", definition.ID, entries, total)
		}
	}
}

func TestIDEFixturesCarryDebugAndMixedDocumentCanaries(t *testing.T) {
	for file, token := range map[string]string{
		"fixtures/jvm-java-spring/src/main/java/dev/mds/GreetingController.java":   "inspected",
		"fixtures/jvm-kotlin-spring/src/main/kotlin/dev/mds/GreetingController.kt": "inspected",
		"fixtures/dotnet-console-test/Probe.cs":                                    "inspected",
		"fixtures/dotnet-mvc-razor/Pages/Index.cshtml":                             "@Model.Greeting",
		"fixtures/dotnet-blazor/Components/Pages/Counter.razor":                    "@onclick",
	} {
		content, err := ideFixtures.ReadFile(file)
		if err != nil || !strings.Contains(string(content), token) {
			t.Fatalf("fixture %s omits %q: %v", file, token, err)
		}
	}
}
