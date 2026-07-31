package contracts_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/packages"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
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

func TestCatalogVerificationCommandsStayInsideNonPrivilegedProbeAllowlist(
	t *testing.T,
) {
	environment := loadCatalog(t)
	for _, component := range environment.Catalog.Components {
		for _, argv := range [][]string{
			component.Verification.Command,
			component.Verification.Functional,
		} {
			if len(argv) == 0 {
				continue
			}
			err := packages.ValidateCatalogVerificationCommand(
				component.ID,
				transport.Command{
					Executable: argv[0],
					Arguments:  argv[1:],
				},
			)
			if err != nil {
				t.Fatalf(
					"component %s verification %v is not safe: %v",
					component.ID,
					argv,
					err,
				)
			}
		}
	}
}

func TestCatalogRevisionBindsGuestImageIdentity(t *testing.T) {
	environment := loadCatalog(t)
	before, err := catalog.Revision(environment)
	if err != nil {
		t.Fatalf("Revision(before): %v", err)
	}
	specification := environment.Targets["ubuntu-26.04"]
	specification.WSLImages = map[string]catalog.ImageSpec{
		"amd64": specification.WSLImages["amd64"],
		"arm64": specification.WSLImages["arm64"],
	}
	arm64 := specification.WSLImages["arm64"]
	arm64.SHA256 = strings.Repeat("0", 64)
	specification.WSLImages["arm64"] = arm64
	environment.Targets = map[string]catalog.TargetSpec{
		"ubuntu-26.04": specification,
	}
	after, err := catalog.Revision(environment)
	if err != nil {
		t.Fatalf("Revision(after): %v", err)
	}
	if before == after {
		t.Fatal("catalog revision did not bind changed WSL image digest")
	}
}

func TestCatalogRevisionBindsExactMiseInputs(t *testing.T) {
	environment := loadCatalog(t)
	before, err := catalog.Revision(environment)
	if err != nil {
		t.Fatalf("Revision(before): %v", err)
	}
	environment.Mise.Lock += "\n# reviewed identity change\n"
	after, err := catalog.Revision(environment)
	if err != nil {
		t.Fatalf("Revision(after): %v", err)
	}
	if before == after {
		t.Fatal("catalog revision did not bind mise.lock bytes")
	}
}

func TestCatalogLoadNormalizesMiseLineEndings(t *testing.T) {
	source := filepath.Join(repositoryRoot(t), "catalog")
	fixture := t.TempDir()
	if err := os.CopyFS(fixture, os.DirFS(source)); err != nil {
		t.Fatalf("CopyFS(catalog): %v", err)
	}
	for _, name := range []string{"mise.toml", "mise.lock"} {
		path := filepath.Join(fixture, name)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
		crlf := strings.ReplaceAll(normalized, "\n", "\r\n")
		if err := os.WriteFile(path, []byte(crlf), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}

	lfEnvironment := loadCatalog(t)
	crlfEnvironment, err := catalog.Load(fixture)
	if err != nil {
		t.Fatalf("Load(CRLF catalog): %v", err)
	}
	if strings.Contains(crlfEnvironment.Mise.Config, "\r") ||
		strings.Contains(crlfEnvironment.Mise.Lock, "\r") {
		t.Fatal("loaded mise inputs retained checkout-specific CR characters")
	}
	lfRevision, err := catalog.Revision(lfEnvironment)
	if err != nil {
		t.Fatalf("Revision(LF): %v", err)
	}
	crlfRevision, err := catalog.Revision(crlfEnvironment)
	if err != nil {
		t.Fatalf("Revision(CRLF): %v", err)
	}
	if crlfRevision != lfRevision {
		t.Fatalf(
			"checkout line endings changed catalog revision: %s != %s",
			crlfRevision,
			lfRevision,
		)
	}
}

func TestValidationRejectsMiseLockArtifactDrift(t *testing.T) {
	environment := loadCatalog(t)
	environment.Mise.Lock = strings.Replace(
		environment.Mise.Lock,
		"sha256:fe4789e92b1f33358680864bbe8704289e7bb5fc207d80623c308935bd696d49",
		"sha256:"+strings.Repeat("0", 64),
		1,
	)
	assertValidationError(t, environment, `mise lock tool "go" platform "linux-arm64"`)
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

func TestValidationRejectsReviewedURLQuery(t *testing.T) {
	environment := validEnvironment()
	entry := environment.Lock.Versions["fixture"]
	entry.Provenance = "https://example.com/fixture?token=secret"
	environment.Lock.Versions["fixture"] = entry

	assertValidationError(t, environment, "without a query or fragment")
}

func TestPublishedSchemasAndSemanticValidationRejectSameCoreDrift(t *testing.T) {
	for _, test := range []struct {
		name       string
		schemaPath string
		document   func(catalog.Environment) any
		mutate     func(*catalog.Environment)
	}{
		{
			name:       "pinned policy without lock key",
			schemaPath: "environment.schema.json",
			document: func(environment catalog.Environment) any {
				return environment.Catalog
			},
			mutate: func(environment *catalog.Environment) {
				environment.Catalog.Components[0].VersionPolicy.LockKey = ""
			},
		},
		{
			name:       "supported target without installer",
			schemaPath: "environment.schema.json",
			document: func(environment catalog.Environment) any {
				return environment.Catalog
			},
			mutate: func(environment *catalog.Environment) {
				cell := environment.Catalog.Components[0].Targets[catalog.TargetLimaGuest]
				cell.Installer = ""
				environment.Catalog.Components[0].Targets[catalog.TargetLimaGuest] = cell
			},
		},
		{
			name:       "query-bearing provenance",
			schemaPath: "lock.schema.json",
			document: func(environment catalog.Environment) any {
				return environment.Lock
			},
			mutate: func(environment *catalog.Environment) {
				entry := environment.Lock.Versions["fixture"]
				entry.Provenance = "https://example.com/fixture?token=secret"
				environment.Lock.Versions["fixture"] = entry
			},
		},
		{
			name:       "unknown component kind",
			schemaPath: "environment.schema.json",
			document: func(environment catalog.Environment) any {
				return environment.Catalog
			},
			mutate: func(environment *catalog.Environment) {
				environment.Catalog.Components[0].Kind = "unknown"
			},
		},
		{
			name:       "invalid component identifier",
			schemaPath: "environment.schema.json",
			document: func(environment catalog.Environment) any {
				return environment.Catalog
			},
			mutate: func(environment *catalog.Environment) {
				environment.Catalog.Components[0].ID = "Invalid_ID"
			},
		},
		{
			name:       "empty verification argument",
			schemaPath: "environment.schema.json",
			document: func(environment catalog.Environment) any {
				return environment.Catalog
			},
			mutate: func(environment *catalog.Environment) {
				environment.Catalog.Components[0].Verification.Command = []string{""}
			},
		},
		{
			name:       "invalid artifact platform identifier",
			schemaPath: "lock.schema.json",
			document: func(environment catalog.Environment) any {
				return environment.Lock
			},
			mutate: func(environment *catalog.Environment) {
				entry := environment.Lock.Versions["fixture"]
				entry.Artifacts = map[string]catalog.Artifact{
					"Linux_AMD64": {
						URL:        "https://example.com/fixture",
						SHA256:     strings.Repeat("a", 64),
						Format:     "binary",
						Executable: "fixture",
					},
				}
				environment.Lock.Versions["fixture"] = entry
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			environment := validEnvironment()
			test.mutate(&environment)
			if err := catalog.Validate(environment); err == nil {
				t.Fatal("semantic validation accepted invalid fixture")
			}
			if schemaAccepts(t, test.schemaPath, test.document(environment)) {
				t.Fatal("published JSON Schema accepted semantically invalid fixture")
			}
		})
	}
}

func TestPublishedTargetSchemaRejectsExplicitEmptyAndIncompatibleCells(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "explicit empty installer",
			mutate: func(cell map[string]any) {
				cell["installer"] = ""
			},
		},
		{
			name: "explicit empty package",
			mutate: func(cell map[string]any) {
				cell["package"] = ""
			},
		},
		{
			name: "explicit empty reason",
			mutate: func(cell map[string]any) {
				cell["status"] = "unsupported"
				delete(cell, "installer")
				delete(cell, "package")
				cell["reason"] = ""
			},
		},
		{
			name: "target incompatible installer",
			mutate: func(cell map[string]any) {
				cell["installer"] = "winget"
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(validEnvironment().Catalog)
			if err != nil {
				t.Fatalf("Marshal(catalog): %v", err)
			}
			var document map[string]any
			if err := json.Unmarshal(encoded, &document); err != nil {
				t.Fatalf("Unmarshal(catalog): %v", err)
			}
			components := document["components"].([]any)
			component := components[0].(map[string]any)
			targets := component["targets"].(map[string]any)
			cell := targets["lima-guest"].(map[string]any)
			test.mutate(cell)
			if schemaAccepts(t, "environment.schema.json", document) {
				t.Fatal("published JSON Schema accepted invalid raw target cell")
			}
		})
	}
}

func schemaAccepts(t *testing.T, name string, document any) bool {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(
		repositoryRoot(t),
		"catalog",
		"schema",
		name,
	))
	if err != nil {
		t.Fatalf("ReadFile(schema): %v", err)
	}
	var schemaDocument any
	if err := json.Unmarshal(content, &schemaDocument); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(name, schemaDocument); err != nil {
		t.Fatalf("AddResource(): %v", err)
	}
	compiled, err := compiler.Compile(name)
	if err != nil {
		t.Fatalf("Compile(): %v", err)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("Marshal(document): %v", err)
	}
	var jsonDocument any
	if err := json.Unmarshal(encoded, &jsonDocument); err != nil {
		t.Fatalf("decode document: %v", err)
	}
	return compiled.Validate(jsonDocument) == nil
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

func TestCertificationProfilesAreReachableOnExactTargets(t *testing.T) {
	environment := loadCatalog(t)
	tests := []struct {
		profile string
		facts   target.Facts
	}{
		{
			profile: "certification-macos-host",
			facts: certificationFacts(
				t,
				target.KindMacOSHost,
				"local",
				"darwin",
				"arm64",
			),
		},
		{
			profile: "certification-windows-host",
			facts: certificationFacts(
				t,
				target.KindWindowsHost,
				"local",
				"windows",
				"amd64",
			),
		},
		{
			profile: "certification-wsl-guest",
			facts: certificationFacts(
				t,
				target.KindWSLGuest,
				"Ubuntu-26.04",
				"linux",
				"amd64",
			),
		},
		{
			profile: "certification-lima-guest",
			facts: certificationFacts(
				t,
				target.KindLimaGuest,
				"mds",
				"linux",
				"arm64",
			),
		},
	}

	coveredKinds := make(map[string]bool)
	coveredComponents := make(map[string]bool)
	for _, test := range tests {
		t.Run(test.profile, func(t *testing.T) {
			selection, err := planning.Profile(test.profile)
			if err != nil {
				t.Fatalf("Profile(): %v", err)
			}
			plan, err := planning.Build(environment, test.facts, selection)
			if err != nil {
				t.Fatalf("Build(): %v", err)
			}
			if len(plan.Actions) == 0 {
				t.Fatal("certification profile resolved no actions")
			}
			if len(plan.Blockers) != 0 {
				t.Fatalf(
					"certification profile has static blockers: %+v",
					plan.Blockers,
				)
			}
			for _, action := range plan.Actions {
				component := catalogComponentByID(
					t,
					environment,
					action.ComponentID,
				)
				coveredKinds[component.Kind] = true
				coveredComponents[component.ID] = true
			}
		})
	}

	for _, kind := range []string{
		"agent",
		"cli",
		"container",
		"editor",
		"language",
		"platform",
	} {
		if !coveredKinds[kind] {
			t.Fatalf(
				"certification profiles do not cover representative %q surface",
				kind,
			)
		}
	}
	for _, component := range []string{
		"base-cli",
		"codex",
		"docker-engine",
		"go",
		"lima",
		"neovim",
		"wsl",
	} {
		if !coveredComponents[component] {
			t.Fatalf(
				"certification profiles do not cover representative component %q",
				component,
			)
		}
	}
}

func TestCertificationProfilesPreserveAllAndOwnerXcodeTruthfulness(t *testing.T) {
	environment := loadCatalog(t)
	facts := certificationFacts(
		t,
		target.KindMacOSHost,
		"local",
		"darwin",
		"arm64",
	)
	for _, selection := range []struct {
		name      string
		selection planning.Selection
	}{
		{name: "all", selection: planning.All()},
		{
			name: "owner",
			selection: func() planning.Selection {
				value, err := planning.Profile("owner")
				if err != nil {
					t.Fatalf("Profile(owner): %v", err)
				}
				return value
			}(),
		},
	} {
		t.Run(selection.name, func(t *testing.T) {
			plan, err := planning.Build(
				environment,
				facts,
				selection.selection,
			)
			if err != nil {
				t.Fatalf("Build(): %v", err)
			}
			for _, blocker := range plan.Blockers {
				if blocker.ActionID == "macos-host:local/xcode" &&
					blocker.Status == planning.ActionActionRequired {
					return
				}
			}
			t.Fatalf(
				"%s no longer preserves the Xcode action-required blocker: %+v",
				selection.name,
				plan.Blockers,
			)
		})
	}

	certification, err := planning.Profile("certification-macos-host")
	if err != nil {
		t.Fatalf("Profile(certification-macos-host): %v", err)
	}
	plan, err := planning.Build(environment, facts, certification)
	if err != nil {
		t.Fatalf("Build(certification-macos-host): %v", err)
	}
	if len(plan.Blockers) != 0 {
		t.Fatalf(
			"certification macOS profile has static blockers: %+v",
			plan.Blockers,
		)
	}
	for _, action := range plan.Actions {
		if action.ComponentID == "xcode" {
			t.Fatal("certification macOS profile selected manual Xcode")
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

func certificationFacts(
	t *testing.T,
	kind target.Kind,
	name,
	osName,
	architecture string,
) target.Facts {
	t.Helper()
	id, err := target.NewID(kind, name)
	if err != nil {
		t.Fatalf("NewID(): %v", err)
	}
	return target.Facts{
		ID:            id,
		OS:            osName,
		OSVersion:     "fixture",
		Architecture:  architecture,
		ImageRevision: "sha256:fixture",
		SystemdSupported: kind == target.KindWSLGuest ||
			kind == target.KindLimaGuest,
		SystemdActive: kind == target.KindWSLGuest ||
			kind == target.KindLimaGuest,
		Reachable: true,
	}
}

func catalogComponentByID(
	t *testing.T,
	environment catalog.Environment,
	id string,
) catalog.Component {
	t.Helper()
	for _, component := range environment.Catalog.Components {
		if component.ID == id {
			return component
		}
	}
	t.Fatalf("resolved unknown component %q", id)
	return catalog.Component{}
}
