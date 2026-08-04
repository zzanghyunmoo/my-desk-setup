package contracts_test

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
)

func TestHostHarnessFixtureLoadsWithClosedPrePublishIdentity(t *testing.T) {
	environment := loadHostHarnessFixture(t)
	if err := catalog.Validate(environment); err != nil {
		t.Fatalf("Validate(host harness fixture): %v", err)
	}

	node := catalogComponentByID(t, environment, "omh-node-runtime")
	if node.Kind != "build" ||
		node.SelectionPolicy != catalog.SelectionPolicyDependencyOnly {
		t.Fatalf("node fixture = %+v, want dependency-only build component", node)
	}
	if got := environment.Lock.Versions["omh-node-runtime"].Version; got != "22.19.0" {
		t.Fatalf("Node version = %q, want exact 22.19.0", got)
	}

	for _, id := range []string{"claude-code", "opencode", "codex"} {
		entry := environment.Lock.Versions[id]
		if entry.Version == "" || len(entry.Artifacts) != 4 {
			t.Fatalf("%s lock = %+v, want exact version and four host artifacts", id, entry)
		}
		for platform, artifact := range entry.Artifacts {
			if len(artifact.SHA256) != 64 || len(artifact.ExecutableSHA256) != 64 {
				t.Fatalf("%s/%s lacks archive or executable digest: %+v", id, platform, artifact)
			}
		}
	}

	identityPath := filepath.Join(
		repositoryRoot(t),
		"tests", "fixtures", "catalog", "host-harness", "release-identity.json",
	)
	file, err := os.Open(identityPath)
	if err != nil {
		t.Fatalf("Open(release identity): %v", err)
	}
	defer file.Close()
	var identity struct {
		SchemaVersion int    `json:"schema_version"`
		Purpose       string `json:"purpose"`
		Production    bool   `json:"production"`
		Release       struct {
			Version         string `json:"version"`
			Tag             string `json:"tag"`
			ArchiveFilename string `json:"archive_filename"`
			ArchiveSHA256   string `json:"archive_sha256"`
			ArchiveSize     int64  `json:"archive_size"`
			SidecarFilename string `json:"sidecar_filename"`
			SourceCommit    string `json:"source_commit"`
			SourceTree      string `json:"source_tree"`
			CatalogRevision string `json:"catalog_revision"`
		} `json:"release"`
	}
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&identity); err != nil {
		t.Fatalf("Decode(release identity): %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("release identity has trailing data: %v", err)
	}
	if identity.SchemaVersion != 1 || identity.Production ||
		identity.Purpose != "pre-publish-consumer-contract" ||
		identity.Release.Version != "0.3.0" ||
		identity.Release.Tag != "v0.3.0" ||
		identity.Release.ArchiveFilename != "oh-my-harness-v0.3.0.tgz" ||
		identity.Release.ArchiveSHA256 != "e337134899be4eb3e0d229f03fd40b3afb9b3e83b41361c1e670f2e490b5a82c" ||
		identity.Release.ArchiveSize != 4725469 ||
		identity.Release.SidecarFilename != "oh-my-harness-v0.3.0.release.json" ||
		len(identity.Release.SourceCommit) != 40 ||
		len(identity.Release.SourceTree) != 40 ||
		len(identity.Release.CatalogRevision) != 64 {
		t.Fatalf("release fixture identity is not closed and exact: %+v", identity)
	}
}

func TestDependencyOnlySelectionPolicyControlsRootsButNotClosure(t *testing.T) {
	environment := loadHostHarnessFixture(t)

	_, err := catalog.ResolveSelection(
		environment,
		[]string{"omh-node-runtime"},
		catalog.TargetMacOSHost,
	)
	if err == nil || !strings.Contains(err.Error(), "dependency-only") {
		t.Fatalf("ResolveSelection(node) error = %v, want dependency-only rejection", err)
	}

	roots := catalog.SelectionRoots(environment, catalog.TargetMacOSHost)
	rootIDs := make([]string, 0, len(roots))
	for _, root := range roots {
		rootIDs = append(rootIDs, root.ID)
	}
	if reflect.DeepEqual(rootIDs, []string{}) || containsString(rootIDs, "omh-node-runtime") {
		t.Fatalf("host selection roots = %v, dependency-only Node must be hidden", rootIDs)
	}
	for _, candidate := range catalog.SelectionCandidates(environment) {
		if candidate.ID == "omh-node-runtime" {
			t.Fatalf("interactive candidate set exposed dependency-only Node: %+v", candidate)
		}
	}

	harnessOnly, err := catalog.ResolveSelection(
		environment,
		[]string{"oh-my-harness"},
		catalog.TargetMacOSHost,
	)
	if err != nil {
		t.Fatalf("ResolveSelection(harness): %v", err)
	}
	if got, want := resolvedIDs(harnessOnly), map[string]bool{
		"omh-node-runtime": true,
		"oh-my-harness":    true,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("harness closure = %v, want exactly %v", got, want)
	}

	all, err := catalog.ResolveProfile(environment, "all", catalog.TargetMacOSHost)
	if err != nil {
		t.Fatalf("ResolveProfile(all): %v", err)
	}
	if !resolvedIDs(all)["omh-node-runtime"] {
		t.Fatalf("all closure = %v, want Node only through harness dependency", resolvedIDs(all))
	}
}

func TestHostHarnessValidationRejectsDependencyOnlyAndExecutableIdentityDrift(t *testing.T) {
	environment := loadHostHarnessFixture(t)
	environment.Profiles = copyProfiles(environment.Profiles)
	environment.Profiles["invalid"] = catalog.Profile{
		SchemaVersion: 1,
		ID:            "invalid",
		Description:   "Invalid direct dependency-only root.",
		Selection:     []string{"omh-node-runtime"},
	}
	assertValidationError(t, environment, "directly selects dependency-only")

	environment = loadHostHarnessFixture(t)
	entry := environment.Lock.Versions["codex"]
	entry.Artifacts = cloneArtifacts(entry.Artifacts)
	artifact := entry.Artifacts["darwin-arm64"]
	artifact.ExecutableSHA256 = ""
	entry.Artifacts["darwin-arm64"] = artifact
	environment.Lock.Versions["codex"] = entry
	assertValidationError(t, environment, "requires an executable SHA-256")
}

func TestGuestPlanExcludesHostHarnessFixture(t *testing.T) {
	environment := loadHostHarnessFixture(t)
	plan, err := planning.Build(environment, fixedFixtureGuestFacts(t), planning.All())
	if err != nil {
		t.Fatalf("Build(guest all): %v", err)
	}
	for _, id := range plan.Selection {
		if id == "oh-my-harness" || id == "omh-node-runtime" ||
			id == "claude-code" || id == "opencode" || id == "codex" {
			t.Fatalf("guest plan selected host harness component %q: %+v", id, plan.Selection)
		}
	}
}

func loadHostHarnessFixture(t *testing.T) catalog.Environment {
	t.Helper()
	root := repositoryRoot(t)
	fixture := filepath.Join(root, "tests", "fixtures", "catalog", "host-harness")
	staging := t.TempDir()
	if err := os.CopyFS(staging, os.DirFS(fixture)); err != nil {
		t.Fatalf("CopyFS(host harness fixture): %v", err)
	}
	if err := os.MkdirAll(filepath.Join(staging, "schema"), 0o700); err != nil {
		t.Fatalf("MkdirAll(schema): %v", err)
	}
	for _, name := range []string{"environment.schema.json", "lock.schema.json"} {
		content, err := os.ReadFile(filepath.Join(root, "catalog", "schema", name))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(staging, "schema", name), content, 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}
	environment, err := catalog.Load(staging)
	if err != nil {
		t.Fatalf("Load(host harness fixture): %v", err)
	}
	return environment
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func cloneArtifacts(source map[string]catalog.Artifact) map[string]catalog.Artifact {
	result := make(map[string]catalog.Artifact, len(source))
	for platform, artifact := range source {
		result[platform] = artifact
	}
	return result
}

func fixedFixtureGuestFacts(t *testing.T) target.Facts {
	t.Helper()
	id, err := target.NewID(target.KindLimaGuest, "fixture")
	if err != nil {
		t.Fatalf("NewID(): %v", err)
	}
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
