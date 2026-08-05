package cli

import (
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
)

func TestInteractiveLabelsExcludeDependencyOnlyComponents(t *testing.T) {
	environment := catalog.Environment{Catalog: catalog.Catalog{
		SchemaVersion: 1,
		Components: []catalog.Component{
			{ID: "harness", Name: "Harness"},
			{
				ID:              "node",
				Name:            "Internal Node",
				SelectionPolicy: catalog.SelectionPolicyDependencyOnly,
			},
		},
	}}
	labels := interactiveLabels(environment)
	if labels["harness"] != "Harness" {
		t.Fatalf("interactive labels = %v, want harness", labels)
	}
	if _, exposed := labels["node"]; exposed {
		t.Fatalf("interactive labels exposed dependency-only Node: %v", labels)
	}
}
