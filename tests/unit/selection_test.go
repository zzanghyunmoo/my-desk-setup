package unit_test

import (
	"reflect"
	"strings"
	"testing"

	catalogdata "github.com/zzanghyunmoo/my-desk-setup/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
)

func TestSelectionRejectsDuplicateComponents(t *testing.T) {
	_, err := planning.Components([]string{"go", "go"})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("Components() error = %v, want duplicate rejection", err)
	}
}

func TestAllProfileAndComponentsProduceSamePlan(t *testing.T) {
	environment := embeddedCatalog(t)
	facts := fixedGuestFacts()

	var ids []string
	for _, component := range catalog.SelectionRoots(environment, catalog.TargetLimaGuest) {
		ids = append(ids, component.ID)
	}
	environment.Profiles = copyCatalogProfiles(environment.Profiles)
	environment.Profiles["computed-all"] = catalog.Profile{
		SchemaVersion: 1,
		ID:            "computed-all",
		Description:   "Test profile equivalent to computed all.",
		Selection:     append([]string(nil), ids...),
	}

	profile, err := planning.Profile("computed-all")
	if err != nil {
		t.Fatalf("Profile(): %v", err)
	}
	components, err := planning.Components(ids)
	if err != nil {
		t.Fatalf("Components(): %v", err)
	}
	allPlan, err := planning.Build(environment, facts, planning.All())
	if err != nil {
		t.Fatalf("Build(all): %v", err)
	}
	profilePlan, err := planning.Build(environment, facts, profile)
	if err != nil {
		t.Fatalf("Build(profile): %v", err)
	}
	componentPlan, err := planning.Build(environment, facts, components)
	if err != nil {
		t.Fatalf("Build(components): %v", err)
	}

	if !reflect.DeepEqual(allPlan, profilePlan) {
		t.Fatalf("all and profile plans differ")
	}
	if !reflect.DeepEqual(allPlan, componentPlan) {
		t.Fatalf("all and component plans differ")
	}
}

func TestPlannerSeparatesGuestAndHostOwnership(t *testing.T) {
	environment := embeddedCatalog(t)
	guestPlan, err := planning.Build(environment, fixedGuestFacts(), planning.All())
	if err != nil {
		t.Fatalf("Build(guest all): %v", err)
	}
	for _, action := range guestPlan.Actions {
		component := componentByID(environment, action.ComponentID)
		if component.Kind == "gui" {
			t.Fatalf("guest plan contains GUI action %q", action.ComponentID)
		}
	}

	hostID, _ := target.NewID(target.KindMacOSHost, "local")
	hostPlan, err := planning.Build(
		environment,
		target.Facts{ID: hostID, OS: "darwin", Architecture: "arm64", Reachable: true},
		planning.All(),
	)
	if err != nil {
		t.Fatalf("Build(host all): %v", err)
	}
	for _, action := range hostPlan.Actions {
		for _, forbidden := range []string{"java", "kotlin", "go", "python", "flutter", "neovim"} {
			if action.ComponentID == forbidden {
				t.Fatalf("host all contains guest toolchain %q", action.ComponentID)
			}
		}
	}
}

func TestDigestChangesWithTargetPreimage(t *testing.T) {
	environment := embeddedCatalog(t)
	selection, _ := planning.Components([]string{"go"})
	before := fixedGuestFacts()
	after := before
	after.ImageRevision = "sha256:new-image"

	beforePlan, err := planning.Build(environment, before, selection)
	if err != nil {
		t.Fatalf("Build(before): %v", err)
	}
	afterPlan, err := planning.Build(environment, after, selection)
	if err != nil {
		t.Fatalf("Build(after): %v", err)
	}
	if beforePlan.Digest == afterPlan.Digest {
		t.Fatalf("digest did not change with target preimage")
	}
}

func embeddedCatalog(t *testing.T) catalog.Environment {
	t.Helper()
	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		t.Fatalf("LoadFS(embedded): %v", err)
	}
	return environment
}

func fixedGuestFacts() target.Facts {
	id, _ := target.NewID(target.KindLimaGuest, "mds")
	return target.Facts{
		ID:               id,
		OS:               "linux",
		OSVersion:        "26.04",
		Architecture:     "arm64",
		ImageRevision:    "sha256:fixture",
		SystemdSupported: true,
		SystemdActive:    true,
		Reachable:        true,
	}
}

func componentByID(environment catalog.Environment, id string) catalog.Component {
	for _, component := range environment.Catalog.Components {
		if component.ID == id {
			return component
		}
	}
	return catalog.Component{}
}

func copyCatalogProfiles(source map[string]catalog.Profile) map[string]catalog.Profile {
	result := make(map[string]catalog.Profile, len(source))
	for id, profile := range source {
		result[id] = profile
	}
	return result
}
