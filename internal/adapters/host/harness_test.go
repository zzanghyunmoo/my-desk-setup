package host

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	harnessruntime "github.com/zzanghyunmoo/my-desk-setup/internal/harness"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
)

type harnessTestSnapshot struct {
	root       string
	executable string
	closed     *int
	wasClosed  bool
}

func (snapshot *harnessTestSnapshot) Root() string       { return snapshot.root }
func (snapshot *harnessTestSnapshot) Executable() string { return snapshot.executable }
func (snapshot *harnessTestSnapshot) Path(relative string) string {
	return filepath.Join(snapshot.root, filepath.FromSlash(relative))
}
func (snapshot *harnessTestSnapshot) Close() error {
	if snapshot.wasClosed {
		return errors.New("snapshot closed more than once")
	}
	snapshot.wasClosed = true
	*snapshot.closed++
	return nil
}

type harnessTestAcquirer struct {
	t          *testing.T
	root       string
	acquired   int
	closeCount int
}

func (acquirer *harnessTestAcquirer) Acquire(
	_ context.Context,
	request artifact.SnapshotRequest,
) (planning.VerifiedSnapshot, error) {
	acquirer.t.Helper()
	root, err := os.MkdirTemp(acquirer.root, "snapshot-*")
	if err != nil {
		acquirer.t.Fatalf("MkdirTemp(snapshot): %v", err)
	}
	acquirer.acquired++
	executable := filepath.Join(root, filepath.FromSlash(request.Executable))
	if request.Alias != "" {
		executable = filepath.Join(root, "alias", request.Alias)
	}
	for path, content := range map[string]string{
		executable: "#!/bin/sh\nexit 0\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			acquirer.t.Fatalf("MkdirAll(%s): %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
			acquirer.t.Fatalf("WriteFile(%s): %v", path, err)
		}
	}
	if request.ExtractAll {
		for path, content := range map[string]string{
			filepath.Join(root, "package", "dist", "cli", "main.js"): "console.log('fixture')\n",
			filepath.Join(root, "package", "assets", "workflow.txt"): "brainstorm\n",
		} {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				acquirer.t.Fatalf("MkdirAll(%s): %v", path, err)
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				acquirer.t.Fatalf("WriteFile(%s): %v", path, err)
			}
		}
	}
	return &harnessTestSnapshot{
		root: root, executable: executable, closed: &acquirer.closeCount,
	}, nil
}

type harnessTestRuntime struct {
	previewResult harnessruntime.Result
	previewCount  int
	applyCount    int
}

func (runtime *harnessTestRuntime) Preview(
	_ context.Context,
	_ harnessruntime.Request,
) (harnessruntime.Result, error) {
	runtime.previewCount++
	return runtime.previewResult, nil
}

func (runtime *harnessTestRuntime) Apply(
	_ context.Context,
	request harnessruntime.Request,
	expectedDigest string,
) (harnessruntime.ApplyResult, error) {
	runtime.applyCount++
	if _, err := os.Lstat(request.Entrypoint); err != nil {
		return harnessruntime.ApplyResult{}, err
	}
	return harnessruntime.ApplyResult{
		SchemaVersion: runtime.previewResult.SchemaVersion,
		Status:        "ready", Digest: expectedDigest,
		CatalogRevision: runtime.previewResult.CatalogRevision,
		SelectedAgents:  append([]string{}, runtime.previewResult.SelectedAgents...),
		Workflows:       append([]string{}, runtime.previewResult.Workflows...),
		Addons:          append([]harnessruntime.Addon{}, runtime.previewResult.Addons...),
		Ownership:       append([]harnessruntime.Ownership{}, runtime.previewResult.Ownership...),
		ConfigDigest:    runtime.previewResult.ConfigDigest,
	}, nil
}

func TestHarnessPreflightRetainsExactInputsThroughApplyAndCleanup(t *testing.T) {
	home := t.TempDir()
	temporary := t.TempDir()
	environment, base := harnessTestFixture(t)
	acquirer := &harnessTestAcquirer{t: t, root: temporary}
	runtime := &harnessTestRuntime{previewResult: harnessTestPreview("a")}
	composer := harnessTestComposer(home, temporary, acquirer, runtime)
	approved, err := composer.Compose(context.Background(), environment, base)
	if err != nil {
		t.Fatalf("Compose(approved): %v", err)
	}
	if acquirer.closeCount != 3 {
		t.Fatalf("initial snapshot closes = %d, want 3", acquirer.closeCount)
	}
	hostHarness := &Harness{
		Environment: environment, Composer: composer, Runtime: runtime,
		Home: home, Platform: "darwin",
	}
	cleanup, err := hostHarness.Preflight(context.Background(), approved)
	if err != nil {
		t.Fatalf("Preflight(): %v", err)
	}
	if acquirer.closeCount != 3 {
		t.Fatalf("preflight closed retained snapshots early: %d", acquirer.closeCount)
	}
	action := harnessTestAction(t, approved)
	if err := hostHarness.Apply(context.Background(), action); err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	if err := hostHarness.Verify(context.Background(), action); err != nil {
		t.Fatalf("Verify(): %v", err)
	}
	if runtime.applyCount != 1 {
		t.Fatalf("child apply count = %d, want 1", runtime.applyCount)
	}
	generation := hostHarness.generationPath(action)
	for _, path := range []string{
		filepath.Join(generation, harnessMarkerName),
		filepath.Join(generation, "package", "dist", "cli", "main.js"),
		filepath.Join(home, ".local", "bin", "omh"),
	} {
		if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("published path %s is not regular: info=%v err=%v", path, info, err)
		}
	}
	launcher, err := os.ReadFile(filepath.Join(home, ".local", "bin", "omh"))
	if err != nil {
		t.Fatalf("ReadFile(omh): %v", err)
	}
	if strings.Contains(string(launcher), " login") || strings.Contains(string(launcher), " auth") {
		t.Fatalf("launcher invokes authentication: %s", launcher)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup(): %v", err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup(second): %v", err)
	}
	if acquirer.closeCount != 6 {
		t.Fatalf("total snapshot closes = %d, want exactly 6", acquirer.closeCount)
	}
}

func TestHarnessPreflightRejectsStaleChildBeforeFilesystemMutation(t *testing.T) {
	home := t.TempDir()
	temporary := t.TempDir()
	environment, base := harnessTestFixture(t)
	acquirer := &harnessTestAcquirer{t: t, root: temporary}
	runtime := &harnessTestRuntime{previewResult: harnessTestPreview("a")}
	composer := harnessTestComposer(home, temporary, acquirer, runtime)
	approved, err := composer.Compose(context.Background(), environment, base)
	if err != nil {
		t.Fatalf("Compose(approved): %v", err)
	}
	runtime.previewResult = harnessTestPreview("d")
	hostHarness := &Harness{
		Environment: environment, Composer: composer, Runtime: runtime,
		Home: home, Platform: "darwin",
	}
	cleanup, err := hostHarness.Preflight(context.Background(), approved)
	if err == nil || cleanup != nil || !strings.Contains(err.Error(), "no longer recomposes") {
		t.Fatalf("Preflight(stale) = hasCleanup=%t err=%v", cleanup != nil, err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".local")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale preflight mutated home: %v", err)
	}
	if runtime.applyCount != 0 || acquirer.closeCount != 6 {
		t.Fatalf("stale lifecycle apply=%d closes=%d", runtime.applyCount, acquirer.closeCount)
	}
}

func TestHarnessApplyRejectsRetainedSnapshotMutationBeforePublication(t *testing.T) {
	home := t.TempDir()
	temporary := t.TempDir()
	environment, base := harnessTestFixture(t)
	acquirer := &harnessTestAcquirer{t: t, root: temporary}
	runtime := &harnessTestRuntime{previewResult: harnessTestPreview("a")}
	composer := harnessTestComposer(home, temporary, acquirer, runtime)
	approved, err := composer.Compose(context.Background(), environment, base)
	if err != nil {
		t.Fatalf("Compose(approved): %v", err)
	}
	hostHarness := &Harness{
		Environment: environment, Composer: composer, Runtime: runtime,
		Home: home, Platform: "darwin",
	}
	cleanup, err := hostHarness.Preflight(context.Background(), approved)
	if err != nil {
		t.Fatalf("Preflight(): %v", err)
	}
	defer func() {
		if err := cleanup(); err != nil {
			t.Errorf("cleanup(): %v", err)
		}
	}()
	changed := filepath.Join(hostHarness.session.sourceRoot, "package", "assets", "workflow.txt")
	if err := os.WriteFile(changed, []byte("changed\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(snapshot drift): %v", err)
	}
	action := harnessTestAction(t, approved)
	if err := hostHarness.Apply(context.Background(), action); err == nil ||
		!strings.Contains(err.Error(), "changed after plan-wide preflight") {
		t.Fatalf("Apply(snapshot drift) error = %v", err)
	}
	if runtime.applyCount != 0 {
		t.Fatalf("snapshot drift invoked child apply %d times", runtime.applyCount)
	}
	if _, err := os.Lstat(hostHarness.generationPath(action)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("snapshot drift published generation: %v", err)
	}
}

func TestHarnessPreflightRejectsUserOwnedLauncherBeforeAcquisition(t *testing.T) {
	home := t.TempDir()
	temporary := t.TempDir()
	environment, base := harnessTestFixture(t)
	acquirer := &harnessTestAcquirer{t: t, root: temporary}
	runtime := &harnessTestRuntime{previewResult: harnessTestPreview("a")}
	composer := harnessTestComposer(home, temporary, acquirer, runtime)
	approved, err := composer.Compose(context.Background(), environment, base)
	if err != nil {
		t.Fatalf("Compose(approved): %v", err)
	}
	launcher := filepath.Join(home, ".local", "bin", "omh")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("user owned\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	hostHarness := &Harness{
		Environment: environment, Composer: composer, Runtime: runtime,
		Home: home, Platform: "darwin",
	}
	cleanup, err := hostHarness.Preflight(context.Background(), approved)
	var actionRequired *adapters.ActionRequiredError
	if cleanup != nil || !errors.As(err, &actionRequired) {
		t.Fatalf("Preflight(conflict) = hasCleanup=%t err=%v", cleanup != nil, err)
	}
	if acquirer.acquired != 3 {
		t.Fatalf("conflict acquired new snapshots: %d", acquirer.acquired)
	}
}

func harnessTestComposer(
	home,
	temporary string,
	acquirer planning.SnapshotAcquirer,
	runtime *harnessTestRuntime,
) planning.Composer {
	return planning.Composer{
		Acquirer: acquirer, Previewer: runtime,
		Home: home, ConfigRoot: filepath.Join(home, ".config"),
		TempRoot: temporary, StateRoot: filepath.Join(home, ".local", "state", "omh"),
		Locale: "C.UTF-8",
	}
}

func harnessTestPreview(digestCharacter string) harnessruntime.Result {
	return harnessruntime.Result{
		SchemaVersion: "2.0.0", Digest: strings.Repeat(digestCharacter, 64),
		CatalogRevision: strings.Repeat("b", 64), Readiness: "preview",
		SelectedAgents: []string{"codex"},
		Workflows: []string{
			"brainstorm", "code-review", "deep-research", "doc-review", "goal",
			"ideation", "plan", "ralph-loop", "security-guidance", "skill-creator",
		},
		Addons: []harnessruntime.Addon{{
			AgentID: "codex", ID: "omo", Version: "4.19.2", State: "installable",
			Fingerprint: strings.Repeat("c", 64),
		}},
		Ownership: []harnessruntime.Ownership{{
			ID: "codex", Ownership: "external", State: "ready",
		}},
		ConfigDigest: "sha256:" + strings.Repeat("c", 64),
		Blockers:     []string{}, OptionalGapIDs: []string{},
	}
}

func harnessTestFixture(t *testing.T) (catalog.Environment, planning.Plan) {
	t.Helper()
	targetID, err := target.NewID(target.KindMacOSHost, "local")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"omh-node-runtime", "oh-my-harness", "codex"}
	components := make([]catalog.Component, 0, len(ids))
	locks := make(map[string]catalog.LockEntry, len(ids))
	for index, id := range ids {
		executable := id
		if id == "omh-node-runtime" {
			executable = "node-v22/bin/node"
		}
		if id == "oh-my-harness" {
			executable = "package/omh"
		}
		artifactValue := catalog.Artifact{
			URL:    "https://example.test/" + id + ".tgz",
			SHA256: strings.Repeat(string(rune('1'+index)), 64),
			Format: "tar.gz", Executable: executable,
		}
		if id == "codex" {
			artifactValue.ExecutableSHA256 = strings.Repeat("e", 64)
		}
		components = append(components, catalog.Component{
			ID: id, VersionPolicy: catalog.VersionPolicy{Mode: "pinned", LockKey: id},
		})
		locks[id] = catalog.LockEntry{
			Version: "1.0." + string(rune('0'+index)), Source: "fixture",
			Provenance: "https://example.test/provenance/" + id,
			Artifacts:  map[string]catalog.Artifact{"darwin-arm64": artifactValue},
		}
	}
	environment := catalog.Environment{
		Catalog: catalog.Catalog{SchemaVersion: 1, Components: components},
		Lock:    catalog.VersionLock{SchemaVersion: 1, Versions: locks},
	}
	plan := planning.Plan{
		SchemaVersion: planning.PlanSchema, CatalogRevision: strings.Repeat("9", 64),
		Target:    target.Facts{ID: targetID, OS: "darwin", Architecture: "arm64"},
		Selection: append([]string{}, ids...), Blockers: []planning.Blocker{},
	}
	for _, id := range ids {
		plan.Actions = append(plan.Actions, planning.Action{
			ID: targetID.String() + "/" + id, ComponentID: id,
			TargetID: targetID.String(), Status: planning.ActionPlanned,
			Version: locks[id].Version, Dependencies: []string{}, Verification: [][]string{},
		})
	}
	plan.Digest, err = planning.Digest(plan)
	if err != nil {
		t.Fatal(err)
	}
	return environment, plan
}

func harnessTestAction(t *testing.T, plan planning.Plan) planning.Action {
	t.Helper()
	for _, action := range plan.Actions {
		if action.ComponentID == "oh-my-harness" {
			return action
		}
	}
	t.Fatal("missing harness action")
	return planning.Action{}
}
