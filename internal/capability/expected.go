package capability

import (
	"slices"
	"sort"
)

type ExpectedCheck struct {
	ID          string
	Kind        Kind
	ComponentID string
}

var expectedByComponent = map[string][]ExpectedCheck{
	"nvim-jvm": {
		{ID: "artifact.jvm", Kind: KindArtifact, ComponentID: "nvim-jvm"},
		{ID: "config.jvm", Kind: KindConfiguration, ComponentID: "nvim-jvm"},
		{ID: "lsp.java", Kind: KindLSP, ComponentID: "nvim-jvm"},
		{ID: "lsp.kotlin", Kind: KindLSP, ComponentID: "nvim-jvm"},
		{ID: "lsp.spring", Kind: KindLSP, ComponentID: "nvim-jvm"},
		{ID: "action.jvm.build", Kind: KindProjectAction, ComponentID: "nvim-jvm"},
		{ID: "action.jvm.test", Kind: KindProjectAction, ComponentID: "nvim-jvm"},
		{ID: "action.jvm.run", Kind: KindProjectAction, ComponentID: "nvim-jvm"},
		{ID: "dap.java.app", Kind: KindDAP, ComponentID: "nvim-jvm"},
		{ID: "dap.java.test", Kind: KindDAP, ComponentID: "nvim-jvm"},
		{ID: "dap.kotlin.app", Kind: KindDAP, ComponentID: "nvim-jvm"},
		{ID: "dap.kotlin.test", Kind: KindDAP, ComponentID: "nvim-jvm"},
		{ID: "actual.lima.jvm", Kind: KindActualTarget, ComponentID: "nvim-jvm"},
	},
	"nvim-dotnet": {
		{ID: "artifact.dotnet", Kind: KindArtifact, ComponentID: "nvim-dotnet"},
		{ID: "config.dotnet", Kind: KindConfiguration, ComponentID: "nvim-dotnet"},
		{ID: "lsp.csharp", Kind: KindLSP, ComponentID: "nvim-dotnet"},
		{ID: "mixed.razor", Kind: KindMixedDocument, ComponentID: "nvim-dotnet"},
		{ID: "mixed.blazor", Kind: KindMixedDocument, ComponentID: "nvim-dotnet"},
		{ID: "action.dotnet.build", Kind: KindProjectAction, ComponentID: "nvim-dotnet"},
		{ID: "action.dotnet.test", Kind: KindProjectAction, ComponentID: "nvim-dotnet"},
		{ID: "action.dotnet.run", Kind: KindProjectAction, ComponentID: "nvim-dotnet"},
		{ID: "action.dotnet.watch", Kind: KindProjectAction, ComponentID: "nvim-dotnet"},
		{ID: "dap.dotnet.app", Kind: KindDAP, ComponentID: "nvim-dotnet"},
		{ID: "dap.dotnet.server", Kind: KindDAP, ComponentID: "nvim-dotnet"},
		{ID: "dap.dotnet.test", Kind: KindDAP, ComponentID: "nvim-dotnet"},
		{ID: "actual.lima.dotnet", Kind: KindActualTarget, ComponentID: "nvim-dotnet"},
	},
}

func Expected(components []string) []ExpectedCheck {
	seen := make(map[string]ExpectedCheck)
	for _, componentID := range components {
		for _, expected := range expectedByComponent[componentID] {
			seen[expected.ID] = expected
		}
	}
	result := make([]ExpectedCheck, 0, len(seen))
	for _, expected := range seen {
		result = append(result, expected)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return result
}

func ExpectedIDs(components []string) []string {
	expected := Expected(components)
	result := make([]string, len(expected))
	for index := range expected {
		result[index] = expected[index].ID
	}
	return result
}

func MatchesExpected(components []string, receipt *Receipt) bool {
	if receipt == nil || receipt.SchemaVersion != SchemaVersion {
		return false
	}
	expected := Expected(components)
	if len(expected) == 0 || !slices.Equal(receipt.ExpectedIDs, ExpectedIDs(components)) ||
		len(receipt.Checks) != len(expected) {
		return false
	}
	byID := make(map[string]CapabilityCheck, len(receipt.Checks))
	for _, check := range receipt.Checks {
		byID[check.ID] = check
	}
	for _, specification := range expected {
		check, ok := byID[specification.ID]
		if !ok || check.Kind != specification.Kind || check.ComponentID != specification.ComponentID {
			return false
		}
	}
	recomputed := Aggregate(receipt.ExpectedIDs, receipt.Checks)
	return recomputed.Ready && receipt.Ready && len(receipt.Problems) == 0
}
