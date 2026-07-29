package contracts_test

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
)

func TestCatalogLoadsAndRevisionIsCanonical(t *testing.T) {
	environment := loadCatalog(t)
	first, err := catalog.Revision(environment)
	if err != nil {
		t.Fatalf("first revision: %v", err)
	}

	reversed := environment
	reversed.Catalog.Components = append(
		[]catalog.Component(nil),
		environment.Catalog.Components...,
	)
	for left, right := 0, len(reversed.Catalog.Components)-1; left < right; left, right = left+1, right-1 {
		reversed.Catalog.Components[left], reversed.Catalog.Components[right] =
			reversed.Catalog.Components[right], reversed.Catalog.Components[left]
	}
	owner := reversed.Profiles["owner"]
	owner.Selection = append([]string(nil), owner.Selection...)
	for left, right := 0, len(owner.Selection)-1; left < right; left, right = left+1, right-1 {
		owner.Selection[left], owner.Selection[right] = owner.Selection[right], owner.Selection[left]
	}
	reversed.Profiles = copyProfiles(reversed.Profiles)
	reversed.Profiles["owner"] = owner

	second, err := catalog.Revision(reversed)
	if err != nil {
		t.Fatalf("second revision: %v", err)
	}
	if first != second {
		t.Fatalf("revision changed after reordering: %q != %q", first, second)
	}
	if !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("revision = %q, want sha256 prefix", first)
	}
}

func TestStrictYAMLRejectsUnknownField(t *testing.T) {
	fixture := filepath.Join(repositoryRoot(t), "tests", "fixtures", "catalog", "unknown-field")
	_, err := catalog.Load(fixture)
	if err == nil || !strings.Contains(err.Error(), "field unexpected not found") {
		t.Fatalf("Load() error = %v, want strict unknown-field error", err)
	}
}

func TestValidationRejectsDuplicateCapabilityOwner(t *testing.T) {
	environment := validEnvironment()
	duplicate := environment.Catalog.Components[0]
	duplicate.ID = "duplicate"
	duplicate.VersionPolicy.LockKey = "duplicate"
	environment.Lock.Versions["duplicate"] = catalog.LockEntry{
		Version: "1.0.0", Source: "fixture", Provenance: "https://example.com/duplicate",
	}
	environment.Catalog.Components = append(environment.Catalog.Components, duplicate)

	assertValidationError(t, environment, "duplicate owners")
}

func TestValidationRejectsDependencyCycle(t *testing.T) {
	environment := validEnvironment()
	second := fixtureComponent("second", "second-capability", "second")
	environment.Lock.Versions["second"] = catalog.LockEntry{
		Version: "1.0.0", Source: "fixture", Provenance: "https://example.com/second",
	}
	environment.Catalog.Components[0].Dependencies = []string{"second"}
	second.Dependencies = []string{"fixture"}
	environment.Catalog.Components = append(environment.Catalog.Components, second)

	assertValidationError(t, environment, "dependency cycle")
}

func TestValidationRejectsTargetInstallerMismatch(t *testing.T) {
	environment := validEnvironment()
	cell := environment.Catalog.Components[0].Targets[catalog.TargetWindowsHost]
	cell.Installer = "brew"
	environment.Catalog.Components[0].Targets[catalog.TargetWindowsHost] = cell

	assertValidationError(t, environment, "cannot use installer")
}

func TestValidationRejectsCredentialLikeMaterial(t *testing.T) {
	environment := validEnvironment()
	environment.Profiles["minimal"] = catalog.Profile{
		SchemaVersion: 1,
		ID:            "minimal",
		Description:   "token=do-not-store-this",
		Selection:     []string{"fixture"},
	}

	assertValidationError(t, environment, "credential-like material")
}

func TestNotionCLISelectionDoesNotResolveDesktop(t *testing.T) {
	environment := loadCatalog(t)
	resolved, err := catalog.ResolveSelection(
		environment,
		[]string{"notion-cli"},
		catalog.TargetWSLGuest,
	)
	if err != nil {
		t.Fatalf("ResolveSelection(): %v", err)
	}

	ids := resolvedIDs(resolved)
	if !ids["notion-cli"] || !ids["bun"] {
		t.Fatalf("resolved ids = %v, want notion-cli and bun", ids)
	}
	if ids["notion-desktop"] {
		t.Fatalf("notion-cli unexpectedly resolved notion-desktop: %v", ids)
	}
}

func TestGuestAllExcludesGUI(t *testing.T) {
	environment := loadCatalog(t)
	resolved, err := catalog.ResolveProfile(environment, "all", catalog.TargetLimaGuest)
	if err != nil {
		t.Fatalf("ResolveProfile(all): %v", err)
	}
	for _, item := range resolved {
		if item.Component.Kind == "gui" {
			t.Fatalf("guest all contains GUI component %q", item.Component.ID)
		}
		if item.Support.Status == catalog.StatusUnsupported {
			t.Fatalf("guest all contains unsupported component %q", item.Component.ID)
		}
	}
}

func TestNewProfileCompositionNeedsNoCoreChange(t *testing.T) {
	environment := loadCatalog(t)
	environment.Profiles = copyProfiles(environment.Profiles)
	environment.Profiles["writing"] = catalog.Profile{
		SchemaVersion: 1,
		ID:            "writing",
		Description:   "Documentation-focused guest tools.",
		Selection:     []string{"notion-cli", "gh", "neovim"},
	}
	if err := catalog.Validate(environment); err != nil {
		t.Fatalf("Validate(custom profile): %v", err)
	}
	resolved, err := catalog.ResolveProfile(environment, "writing", catalog.TargetWSLGuest)
	if err != nil {
		t.Fatalf("ResolveProfile(writing): %v", err)
	}
	ids := resolvedIDs(resolved)
	for _, id := range []string{"notion-cli", "gh", "neovim"} {
		if !ids[id] {
			t.Fatalf("resolved ids = %v, missing %q", ids, id)
		}
	}
}

func validEnvironment() catalog.Environment {
	component := fixtureComponent("fixture", "fixture-capability", "fixture")
	return catalog.Environment{
		Catalog: catalog.Catalog{
			SchemaVersion: 1,
			Components:    []catalog.Component{component},
		},
		Profiles: map[string]catalog.Profile{
			"minimal": {
				SchemaVersion: 1,
				ID:            "minimal",
				Description:   "Fixture profile.",
				Selection:     []string{"fixture"},
			},
		},
		Lock: catalog.VersionLock{
			SchemaVersion: 1,
			Versions: map[string]catalog.LockEntry{
				"fixture": {
					Version: "1.0.0", Source: "fixture", Provenance: "https://example.com/fixture",
				},
			},
		},
	}
}

func fixtureComponent(id, capability, lockKey string) catalog.Component {
	targets := make(map[catalog.TargetKind]catalog.TargetSupport)
	for _, target := range catalog.TargetKinds {
		installer := "script"
		if target == catalog.TargetWindowsHost {
			installer = "winget"
		}
		targets[target] = catalog.TargetSupport{
			Status: catalog.StatusSupported, Installer: installer, Package: id,
		}
	}
	return catalog.Component{
		ID:           id,
		Name:         id,
		Kind:         "cli",
		Provides:     []string{capability},
		Dependencies: []string{},
		VersionPolicy: catalog.VersionPolicy{
			Mode: "pinned", LockKey: lockKey,
		},
		Verification: catalog.Verification{Command: []string{id, "--version"}},
		Targets:      targets,
	}
}

func assertValidationError(t *testing.T, environment catalog.Environment, want string) {
	t.Helper()
	err := catalog.Validate(environment)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Validate() error = %v, want substring %q", err, want)
	}
}

func loadCatalog(t *testing.T) catalog.Environment {
	t.Helper()
	environment, err := catalog.Load(filepath.Join(repositoryRoot(t), "catalog"))
	if err != nil {
		t.Fatalf("Load(catalog): %v", err)
	}
	return environment
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func copyProfiles(source map[string]catalog.Profile) map[string]catalog.Profile {
	copy := make(map[string]catalog.Profile, len(source))
	for id, profile := range source {
		copy[id] = profile
	}
	return copy
}

func resolvedIDs(resolved []catalog.ResolvedComponent) map[string]bool {
	ids := make(map[string]bool, len(resolved))
	for _, item := range resolved {
		ids[item.Component.ID] = true
	}
	return ids
}
