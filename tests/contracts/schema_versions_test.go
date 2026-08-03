package contracts_test

import (
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/doctor"
	"github.com/zzanghyunmoo/my-desk-setup/internal/evidence"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/release"
	updateflow "github.com/zzanghyunmoo/my-desk-setup/internal/update"
)

func TestPersistedTargetIdentitySchemasArePinnedToVersionTwo(t *testing.T) {
	for name, schema := range map[string]string{
		"plan":              planning.PlanSchema,
		"doctor":            doctor.SchemaVersion,
		"update":            updateflow.PlanSchema,
		"target evidence":   evidence.SchemaVersion,
		"release promotion": release.PromotionSchemaVersion,
	} {
		if schema != "mds."+schemaComponent(name)+"/v2" {
			t.Fatalf("%s schema = %q, want persisted v2 contract", name, schema)
		}
	}
}

func schemaComponent(name string) string {
	switch name {
	case "plan":
		return "plan"
	case "doctor":
		return "doctor"
	case "update":
		return "update"
	case "target evidence":
		return "target-evidence"
	case "release promotion":
		return "release-promotion"
	default:
		panic("unknown schema contract " + name)
	}
}
