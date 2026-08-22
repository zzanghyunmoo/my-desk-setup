package guest

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// ideFixtures is the source authority copied into isolated capability-probe
// workspaces. Dependency caches are deliberately separate managed inputs.
//
//go:embed fixtures/**
var ideFixtures embed.FS

type fixtureDefinition struct {
	ID            string
	Root          string
	RequiredFiles []string
}

var ideFixtureDefinitions = []fixtureDefinition{
	{ID: "jvm-java-spring", Root: "fixtures/jvm-java-spring", RequiredFiles: []string{
		"build.gradle.kts", "gradle/wrapper/gradle-wrapper.jar", "gradle/wrapper/gradle-wrapper.properties",
		"gradlew", "gradlew.bat", "settings.gradle.kts", "src/main/java/dev/mds/Application.java", "src/main/java/dev/mds/DebugProbe.java",
		"src/main/java/dev/mds/GreetingController.java",
		"src/main/resources/application.properties", "src/test/java/dev/mds/GreetingControllerTest.java",
	}},
	{ID: "jvm-kotlin-spring", Root: "fixtures/jvm-kotlin-spring", RequiredFiles: []string{
		"build.gradle.kts", "gradle/wrapper/gradle-wrapper.jar", "gradle/wrapper/gradle-wrapper.properties",
		"gradlew", "gradlew.bat", "settings.gradle.kts", "src/main/kotlin/dev/mds/Application.kt", "src/main/kotlin/dev/mds/DebugProbe.kt",
		"src/main/kotlin/dev/mds/GreetingController.kt",
		"src/main/resources/application.yml", "src/test/kotlin/dev/mds/GreetingControllerTest.kt",
	}},
	{ID: "dotnet-console-test", Root: "fixtures/dotnet-console-test", RequiredFiles: []string{
		"Mds.Console.csproj", "Probe.cs", "Program.cs", "tests/Mds.Console.Tests.csproj", "tests/ProbeTests.cs",
	}},
	{ID: "dotnet-webapi", Root: "fixtures/dotnet-webapi", RequiredFiles: []string{
		"Mds.WebApi.csproj", "Program.cs", "Properties/launchSettings.json",
	}},
	{ID: "dotnet-mvc-razor", Root: "fixtures/dotnet-mvc-razor", RequiredFiles: []string{
		"Mds.Mvc.csproj", "Pages/Index.cshtml", "Pages/Index.cshtml.cs", "Program.cs", "Properties/launchSettings.json",
	}},
	{ID: "dotnet-blazor", Root: "fixtures/dotnet-blazor", RequiredFiles: []string{
		"Components/App.razor", "Components/Pages/Counter.razor", "Mds.Blazor.csproj", "Program.cs", "Properties/launchSettings.json",
	}},
}

func validateIDEFixtures() error {
	probeInfo, probeErr := fs.Stat(ideFixtures, "fixtures/probes/jvm-dap.lua")
	if probeErr != nil || !probeInfo.Mode().IsRegular() {
		return errors.New("IDE fixture DAP probe is missing")
	}
	seen := make(map[string]bool, len(ideFixtureDefinitions))
	for _, definition := range ideFixtureDefinitions {
		if definition.ID == "" || seen[definition.ID] || definition.Root != "fixtures/"+definition.ID {
			return fmt.Errorf("invalid IDE fixture identity %q", definition.ID)
		}
		seen[definition.ID] = true
		if len(definition.RequiredFiles) == 0 || !sort.StringsAreSorted(definition.RequiredFiles) {
			return fmt.Errorf("IDE fixture %s required files are not a non-empty sorted set", definition.ID)
		}
		for _, relative := range definition.RequiredFiles {
			if relative == "" || path.Clean(relative) != relative || strings.HasPrefix(relative, "../") {
				return fmt.Errorf("IDE fixture %s has unsafe path %q", definition.ID, relative)
			}
			info, err := fs.Stat(ideFixtures, definition.Root+"/"+relative)
			if err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("IDE fixture %s is missing %s", definition.ID, relative)
			}
		}
	}
	if len(seen) != 6 {
		return errors.New("IDE fixture set is incomplete")
	}
	return nil
}
