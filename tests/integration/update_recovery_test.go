package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	catalogdata "github.com/zzanghyunmoo/my-desk-setup/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/cli"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/state"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	updateflow "github.com/zzanghyunmoo/my-desk-setup/internal/update"
	"gopkg.in/yaml.v3"
)

func TestExactUpdateMovesLockAndTargetTogether(t *testing.T) {
	catalogRoot := copyEmbeddedCatalog(t)
	environment, err := catalog.Load(catalogRoot)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	id, _ := target.NewID(target.KindLimaGuest, "mds")
	facts := target.Facts{
		ID: id, OS: "linux", OSVersion: "26.04", Architecture: "arm64",
		SystemdSupported: true, SystemdActive: true, Reachable: true,
		CLIRevision: "dev",
	}
	plan, _, err := updateflow.Build(
		environment,
		facts,
		integrationNPMCandidate("6.0.3"),
	)
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	stateRoot := filepath.Join(t.TempDir(), "state")
	result, err := updateflow.Apply(
		context.Background(),
		plan,
		plan.Digest,
		catalogRoot,
		stateRoot,
		testRunner(newFakeAdapter()),
	)
	if err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	if !result.Receipt.Complete ||
		result.CatalogRevision != plan.AfterCatalogRevision {
		t.Fatalf("result = %+v", result)
	}
	updated, err := catalog.Load(catalogRoot)
	if err != nil {
		t.Fatalf("Load(updated): %v", err)
	}
	if got := updated.Lock.Versions["typescript"].Version; got != "6.0.3" {
		t.Fatalf("TypeScript lock = %s, want 6.0.3", got)
	}
}

func TestFailedUpdateKeepsOldLockAndResumesFromIntent(t *testing.T) {
	catalogRoot := copyEmbeddedCatalog(t)
	environment, plan, facts := integrationUpdatePlan(t, catalogRoot, "6.0.3")
	stateRoot := filepath.Join(t.TempDir(), "state")
	paths, err := state.NewPaths(stateRoot, facts.ID.String())
	if err != nil {
		t.Fatalf("NewPaths(): %v", err)
	}
	if err := paths.Ensure(); err != nil {
		t.Fatalf("Ensure(): %v", err)
	}
	previous := state.Receipt{
		PlanDigest:      "sha256:previous-complete",
		CatalogRevision: environment.Lock.Versions["typescript"].Version,
		TargetID:        facts.ID.String(),
		Complete:        true,
	}
	previousPath, err := state.WriteReceipt(paths.Receipts, previous)
	if err != nil {
		t.Fatalf("WriteReceipt(previous): %v", err)
	}
	previousContent, err := os.ReadFile(previousPath)
	if err != nil {
		t.Fatalf("ReadFile(previous): %v", err)
	}

	adapter := newFakeAdapter()
	adapter.failOnce["typescript"] = true
	first, err := updateflow.Apply(
		context.Background(),
		plan,
		plan.Digest,
		catalogRoot,
		stateRoot,
		testRunner(adapter),
	)
	if err != nil {
		t.Fatalf("Apply(first): %v", err)
	}
	if first.Receipt.Complete {
		t.Fatalf("first receipt = %+v, want incomplete", first.Receipt)
	}
	assertCatalogVersion(t, catalogRoot, environment.Lock.Versions["typescript"].Version)
	assertIntentPhase(t, stateRoot, "prepared")
	if after, err := os.ReadFile(previousPath); err != nil {
		t.Fatalf("ReadFile(previous after failure): %v", err)
	} else if !bytes.Equal(after, previousContent) {
		t.Fatal("failed update changed the previous complete receipt")
	}

	second, err := updateflow.Apply(
		context.Background(),
		plan,
		plan.Digest,
		catalogRoot,
		stateRoot,
		testRunner(adapter),
	)
	if err != nil {
		t.Fatalf("Apply(resume): %v", err)
	}
	if !second.Receipt.Complete {
		t.Fatalf("resume receipt = %+v, want complete", second.Receipt)
	}
	assertCatalogVersion(t, catalogRoot, "6.0.3")
	assertIntentPhase(t, stateRoot, "complete")
	if got := adapter.applyCount["typescript"]; got != 2 {
		t.Fatalf("adapter apply count = %d, want failed attempt plus resume", got)
	}
}

func TestUpdateCrashAfterTargetMutationResumesBeforeLockPublication(t *testing.T) {
	catalogRoot := copyEmbeddedCatalog(t)
	environment, plan, _ := integrationUpdatePlan(t, catalogRoot, "6.0.3")
	stateRoot := filepath.Join(t.TempDir(), "state")
	adapter := newFakeAdapter()
	crash := errors.New("simulated update crash")
	runner := testRunner(adapter)
	runner.Hooks.AfterApply = func(action planning.Action) error {
		if action.ComponentID != "typescript" {
			return nil
		}
		return crash
	}

	if _, err := updateflow.Apply(
		context.Background(),
		plan,
		plan.Digest,
		catalogRoot,
		stateRoot,
		runner,
	); !errors.Is(err, crash) {
		t.Fatalf("Apply(crash) error = %v, want simulated crash", err)
	}
	assertCatalogVersion(t, catalogRoot, environment.Lock.Versions["typescript"].Version)
	assertIntentPhase(t, stateRoot, "prepared")

	recovered, err := updateflow.Apply(
		context.Background(),
		plan,
		plan.Digest,
		catalogRoot,
		stateRoot,
		testRunner(adapter),
	)
	if err != nil {
		t.Fatalf("Apply(recovery): %v", err)
	}
	var typescriptNoop bool
	for _, outcome := range recovered.Receipt.Outcomes {
		if strings.HasSuffix(outcome.ActionID, "/typescript") {
			typescriptNoop = outcome.Noop
		}
	}
	if !recovered.Receipt.Complete || !typescriptNoop {
		t.Fatalf("recovery receipt = %+v, want complete no-op", recovered.Receipt)
	}
	assertCatalogVersion(t, catalogRoot, "6.0.3")
	assertIntentPhase(t, stateRoot, "complete")
	if got := adapter.applyCount["typescript"]; got != 1 {
		t.Fatalf("installer repeated after crash: count=%d", got)
	}
}

func TestUpdateResumesAfterLockPublicationBeforeIntentCompletion(t *testing.T) {
	catalogRoot := copyEmbeddedCatalog(t)
	_, plan, _ := integrationUpdatePlan(t, catalogRoot, "6.0.3")
	stateRoot := filepath.Join(t.TempDir(), "state")
	adapter := newFakeAdapter()

	if _, err := updateflow.Apply(
		context.Background(),
		plan,
		plan.Digest,
		catalogRoot,
		stateRoot,
		testRunner(adapter),
	); err != nil {
		t.Fatalf("Apply(initial): %v", err)
	}
	assertCatalogVersion(t, catalogRoot, "6.0.3")
	setIntentPhase(t, stateRoot, "target-reconciled")

	resumedPlan, updated, resumed, err := updateflow.ResumePlan(
		catalogRoot,
		stateRoot,
		plan.Digest,
	)
	if err != nil {
		t.Fatalf("ResumePlan(): %v", err)
	}
	if !resumed || resumedPlan.Digest != plan.Digest {
		t.Fatalf(
			"ResumePlan() resumed=%t digest=%q, want true %q",
			resumed,
			resumedPlan.Digest,
			plan.Digest,
		)
	}
	if got := updated.Lock.Versions["typescript"].Version; got != "6.0.3" {
		t.Fatalf("resumed environment lock = %s, want 6.0.3", got)
	}

	recovered, err := updateflow.Apply(
		context.Background(),
		resumedPlan,
		resumedPlan.Digest,
		catalogRoot,
		stateRoot,
		testRunner(adapter),
	)
	if err != nil {
		t.Fatalf("Apply(recovery): %v", err)
	}
	if !recovered.Receipt.Complete {
		t.Fatalf("recovery receipt = %+v, want complete", recovered.Receipt)
	}
	assertCatalogVersion(t, catalogRoot, "6.0.3")
	assertIntentPhase(t, stateRoot, "complete")
	if got := adapter.applyCount["typescript"]; got != 1 {
		t.Fatalf("installer repeated after lock publication: count=%d", got)
	}
}

func TestUpdateCatalogPreimageDriftIsTypedStale(t *testing.T) {
	catalogRoot := copyEmbeddedCatalog(t)
	_, plan, _ := integrationUpdatePlan(t, catalogRoot, "6.0.3")
	mutateCatalogSource(t, catalogRoot, "local concurrent edit")

	_, err := updateflow.Apply(
		context.Background(),
		plan,
		plan.Digest,
		catalogRoot,
		filepath.Join(t.TempDir(), "state"),
		testRunner(newFakeAdapter()),
	)
	if got := updateflow.KindOf(err); got != updateflow.ErrorStale {
		t.Fatalf("Apply(preimage drift) kind = %q, want %q; err=%v", got, updateflow.ErrorStale, err)
	}
}

func TestUpdateRejectsReadOnlyCatalogBeforeTargetMutation(t *testing.T) {
	catalogRoot := copyEmbeddedCatalog(t)
	_, plan, _ := integrationUpdatePlan(t, catalogRoot, "6.0.3")
	lockPath := filepath.Join(catalogRoot, "locks", "versions.lock.yaml")
	if err := os.Chmod(lockPath, 0o400); err != nil {
		t.Fatalf("Chmod(read-only lock): %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(lockPath, 0o600); err != nil {
			t.Errorf("restore lock mode: %v", err)
		}
	})
	adapter := newFakeAdapter()
	stateRoot := filepath.Join(t.TempDir(), "state")

	_, err := updateflow.Apply(
		context.Background(),
		plan,
		plan.Digest,
		catalogRoot,
		stateRoot,
		testRunner(adapter),
	)
	if got := updateflow.KindOf(err); got != updateflow.ErrorInvalid {
		t.Fatalf("Apply(read-only catalog) kind = %q, want %q; err=%v", got, updateflow.ErrorInvalid, err)
	}
	if len(adapter.applyCount) != 0 {
		t.Fatalf("adapter mutated with read-only catalog: %v", adapter.applyCount)
	}
	if _, statErr := os.Stat(stateRoot); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("state root exists after read-only catalog rejection: %v", statErr)
	}
}

func TestCLIUpdateCatalogPreimageDriftExitsStalePlan(t *testing.T) {
	catalogRoot := copyEmbeddedCatalog(t)
	_, plan, _ := integrationUpdatePlan(t, catalogRoot, "6.0.3")
	candidatePath := filepath.Join(t.TempDir(), "candidate.json")
	candidateContent, err := json.Marshal(integrationNPMCandidate("6.0.3"))
	if err != nil {
		t.Fatalf("Marshal(candidate): %v", err)
	}
	if err := os.WriteFile(candidatePath, candidateContent, 0o600); err != nil {
		t.Fatalf("WriteFile(candidate): %v", err)
	}
	home := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := cli.Run(
		[]string{
			"update",
			"--catalog", catalogRoot,
			"--candidate", candidatePath,
			"--plan-digest", plan.Digest,
			"--state-root", filepath.Join(home, "state"),
			"--format", "json",
		},
		cli.Streams{
			Input: strings.NewReader(""), Output: &stdout, Error: &stderr,
		},
		cli.Runtime{
			GOOS: "linux", GOARCH: "arm64",
			Getenv: func(key string) string {
				if key == "LIMA_INSTANCE" {
					return "mds"
				}
				return ""
			},
			HomeDir: func() (string, error) {
				mutateCatalogSource(t, catalogRoot, "concurrent cli edit")
				return home, nil
			},
		},
	)
	if code != cli.ExitStalePlan {
		t.Fatalf(
			"Run() code=%d, want stale-plan %d; stdout=%q stderr=%q",
			code,
			cli.ExitStalePlan,
			stdout.String(),
			stderr.String(),
		)
	}
	var envelope cli.ErrorEnvelope
	if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v\n%s", err, stderr.String())
	}
	if envelope.Code != "stale-plan" {
		t.Fatalf("error envelope = %+v, want stale-plan", envelope)
	}
}

func TestStaleUpdateDigestMutatesNeitherLockNorState(t *testing.T) {
	catalogRoot := copyEmbeddedCatalog(t)
	environment, err := catalog.Load(catalogRoot)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	id, _ := target.NewID(target.KindLimaGuest, "mds")
	facts := target.Facts{
		ID: id, OS: "linux", OSVersion: "26.04", Architecture: "arm64",
		SystemdSupported: true, SystemdActive: true, Reachable: true,
		CLIRevision: "dev",
	}
	plan, _, err := updateflow.Build(
		environment,
		facts,
		integrationNPMCandidate("6.0.3"),
	)
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	stateRoot := filepath.Join(t.TempDir(), "state")
	if _, err := updateflow.Apply(
		context.Background(),
		plan,
		"sha256:not-reviewed",
		catalogRoot,
		stateRoot,
		testRunner(newFakeAdapter()),
	); err == nil {
		t.Fatal("Apply() accepted stale update digest")
	}
	unchanged, err := catalog.Load(catalogRoot)
	if err != nil {
		t.Fatalf("Load(unchanged): %v", err)
	}
	if got := unchanged.Lock.Versions["typescript"].Version; got !=
		environment.Lock.Versions["typescript"].Version {
		t.Fatalf("TypeScript lock changed to %s after stale digest", got)
	}
	if _, err := os.Stat(stateRoot); !os.IsNotExist(err) {
		t.Fatalf("state root exists after stale update: %v", err)
	}
}

func integrationUpdatePlan(
	t *testing.T,
	catalogRoot,
	version string,
) (catalog.Environment, updateflow.Plan, target.Facts) {
	t.Helper()
	environment, err := catalog.Load(catalogRoot)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	id, _ := target.NewID(target.KindLimaGuest, "mds")
	facts := target.Facts{
		ID: id, OS: "linux", OSVersion: "26.04", Architecture: "arm64",
		SystemdSupported: true, SystemdActive: true, Reachable: true,
		CLIRevision: "dev",
	}
	plan, _, err := updateflow.Build(
		environment,
		facts,
		integrationNPMCandidate(version),
	)
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	return environment, plan, facts
}

func assertCatalogVersion(
	t *testing.T,
	catalogRoot,
	want string,
) {
	t.Helper()
	environment, err := catalog.Load(catalogRoot)
	if err != nil {
		t.Fatalf("Load(catalog): %v", err)
	}
	if got := environment.Lock.Versions["typescript"].Version; got != want {
		t.Fatalf("TypeScript lock = %s, want %s", got, want)
	}
}

func assertIntentPhase(t *testing.T, stateRoot, want string) {
	t.Helper()
	matches, err := filepath.Glob(
		filepath.Join(stateRoot, "catalog-*.update-intent.json"),
	)
	if err != nil {
		t.Fatalf("Glob(intent): %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("intent files = %v, want one", matches)
	}
	content, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("ReadFile(intent): %v", err)
	}
	var intent struct {
		Phase string `json:"phase"`
	}
	if err := json.Unmarshal(content, &intent); err != nil {
		t.Fatalf("decode intent: %v\n%s", err, content)
	}
	if intent.Phase != want {
		t.Fatalf("intent phase = %q, want %q", intent.Phase, want)
	}
}

func setIntentPhase(t *testing.T, stateRoot, phase string) {
	t.Helper()
	matches, err := filepath.Glob(
		filepath.Join(stateRoot, "catalog-*.update-intent.json"),
	)
	if err != nil {
		t.Fatalf("Glob(intent): %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("intent files = %v, want one", matches)
	}
	content, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("ReadFile(intent): %v", err)
	}
	var intent map[string]any
	if err := json.Unmarshal(content, &intent); err != nil {
		t.Fatalf("decode intent: %v\n%s", err, content)
	}
	intent["phase"] = phase
	updated, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		t.Fatalf("Marshal(intent): %v", err)
	}
	updated = append(updated, '\n')
	if err := os.WriteFile(matches[0], updated, 0o600); err != nil {
		t.Fatalf("WriteFile(intent): %v", err)
	}
}

func mutateCatalogSource(t *testing.T, catalogRoot, source string) {
	t.Helper()
	path := filepath.Join(catalogRoot, "locks", "versions.lock.yaml")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(lock): %v", err)
	}
	var lock catalog.VersionLock
	if err := yaml.Unmarshal(content, &lock); err != nil {
		t.Fatalf("Unmarshal(lock): %v", err)
	}
	entry := lock.Versions["typescript"]
	entry.Source = source
	lock.Versions["typescript"] = entry
	updated, err := yaml.Marshal(lock)
	if err != nil {
		t.Fatalf("Marshal(lock): %v", err)
	}
	if err := os.WriteFile(path, updated, 0o600); err != nil {
		t.Fatalf("WriteFile(lock): %v", err)
	}
}

func TestUpdateRejectsSymlinkedLockDirectory(t *testing.T) {
	catalogRoot := copyEmbeddedCatalog(t)
	environment, err := catalog.Load(catalogRoot)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	id, _ := target.NewID(target.KindLimaGuest, "mds")
	facts := target.Facts{
		ID: id, OS: "linux", OSVersion: "26.04", Architecture: "arm64",
		SystemdSupported: true, SystemdActive: true, Reachable: true,
		CLIRevision: "dev",
	}
	plan, _, err := updateflow.Build(
		environment,
		facts,
		integrationNPMCandidate("6.0.3"),
	)
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	outside := t.TempDir()
	lockContent, err := os.ReadFile(filepath.Join(catalogRoot, "locks", "versions.lock.yaml"))
	if err != nil {
		t.Fatalf("read original lock: %v", err)
	}
	outsideLock := filepath.Join(outside, "versions.lock.yaml")
	if err := os.WriteFile(outsideLock, lockContent, 0o600); err != nil {
		t.Fatalf("write outside lock: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(catalogRoot, "locks")); err != nil {
		t.Fatalf("remove copied locks directory: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(catalogRoot, "locks")); err != nil {
		t.Fatalf("symlink locks directory: %v", err)
	}
	_, err = updateflow.Apply(
		context.Background(),
		plan,
		plan.Digest,
		catalogRoot,
		filepath.Join(t.TempDir(), "state"),
		testRunner(newFakeAdapter()),
	)
	if err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("Apply() error = %v, want symlinked directory rejection", err)
	}
	after, err := os.ReadFile(outsideLock)
	if err != nil {
		t.Fatalf("read outside lock after rejection: %v", err)
	}
	if string(after) != string(lockContent) {
		t.Fatal("outside lock changed through symlinked catalog directory")
	}
}

func integrationNPMCandidate(version string) updateflow.Candidate {
	content := []byte("reviewed typescript " + version)
	sha256Sum := sha256.Sum256(content)
	sha512Sum := sha512.Sum512(content)
	return updateflow.Candidate{
		ComponentID: "typescript",
		Version:     version,
		Source:      "npm registry",
		Provenance:  "https://www.npmjs.com/package/typescript/v/" + version,
		NPM: &catalog.NPMArtifact{
			Tarball: "https://registry.npmjs.org/typescript/-/typescript-" +
				version + ".tgz",
			Integrity: "sha512-" +
				base64.StdEncoding.EncodeToString(sha512Sum[:]),
			SHA256: hex.EncodeToString(sha256Sum[:]),
		},
	}
}

func copyEmbeddedCatalog(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	err := fs.WalkDir(catalogdata.FS, ".", func(
		path string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." {
			return nil
		}
		destination := filepath.Join(root, filepath.FromSlash(path))
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		content, err := fs.ReadFile(catalogdata.FS, path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, content, 0o600)
	})
	if err != nil {
		t.Fatalf("copy embedded catalog: %v", err)
	}
	return root
}
