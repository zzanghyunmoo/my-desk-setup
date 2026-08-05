package integration_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/execution"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/state"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
)

func TestApplyRecoversOnlyFailedDependencyScope(t *testing.T) {
	plan := executionPlan(t, "lima-guest:mds")
	adapter := newFakeAdapter()
	adapter.failOnce["b"] = true
	runner := testRunner(adapter)
	stateRoot := filepath.Join(t.TempDir(), "state")

	first, err := runner.Apply(context.Background(), plan, plan.Digest, stateRoot)
	if err != nil {
		t.Fatalf("first Apply(): %v", err)
	}
	firstOutcomes := outcomesByComponent(first)
	if first.Complete {
		t.Fatal("first receipt is complete after a failed action")
	}
	assertStatus(t, firstOutcomes, "a", "ready")
	assertStatus(t, firstOutcomes, "b", "failed")
	assertStatus(t, firstOutcomes, "c", "blocked")
	assertStatus(t, firstOutcomes, "d", "ready")

	second, err := runner.Apply(context.Background(), plan, plan.Digest, stateRoot)
	if err != nil {
		t.Fatalf("second Apply(): %v", err)
	}
	if !second.Complete {
		t.Fatalf("second receipt incomplete: %+v", second.Outcomes)
	}
	secondOutcomes := outcomesByComponent(second)
	if !secondOutcomes["a"].Noop || !secondOutcomes["d"].Noop {
		t.Fatalf("independent ready actions were not no-op: %+v", second.Outcomes)
	}
	if got := adapter.applyCount["a"]; got != 1 {
		t.Fatalf("apply count a = %d, want 1", got)
	}
	if got := adapter.applyCount["b"]; got != 2 {
		t.Fatalf("apply count b = %d, want retry count 2", got)
	}
	if got := adapter.applyCount["c"]; got != 1 {
		t.Fatalf("apply count c = %d, want 1 after dependency recovery", got)
	}
	if got := adapter.applyCount["d"]; got != 1 {
		t.Fatalf("apply count d = %d, want 1", got)
	}
}

func TestCrashAfterInstallBeforeJournalConvergesByObservation(t *testing.T) {
	plan := singleActionPlan(t, "lima-guest:mds")
	adapter := newFakeAdapter()
	stateRoot := filepath.Join(t.TempDir(), "state")
	simulatedCrash := errors.New("simulated crash after installer success")
	crashingRunner := testRunner(adapter)
	crashingRunner.Hooks.AfterApply = func(planning.Action) error {
		return simulatedCrash
	}

	if _, err := crashingRunner.Apply(
		context.Background(),
		plan,
		plan.Digest,
		stateRoot,
	); !errors.Is(err, simulatedCrash) {
		t.Fatalf("crashing Apply() error = %v, want simulated crash", err)
	}
	if got := adapter.applyCount["a"]; got != 1 {
		t.Fatalf("apply count after crash = %d, want 1", got)
	}

	recovered, err := testRunner(adapter).Apply(
		context.Background(),
		plan,
		plan.Digest,
		stateRoot,
	)
	if err != nil {
		t.Fatalf("recovery Apply(): %v", err)
	}
	if !recovered.Complete || !recovered.Outcomes[0].Noop {
		t.Fatalf("recovery receipt = %+v, want complete no-op", recovered)
	}
	if got := adapter.applyCount["a"]; got != 1 {
		t.Fatalf("installer repeated after observed success: count=%d", got)
	}
}

func TestActionRequiredIsRecordedWithoutMislabelingFailure(t *testing.T) {
	plan := singleActionPlan(t, "lima-guest:mds")
	stateRoot := filepath.Join(t.TempDir(), "state")
	runner := testRunner(actionRequiredAdapter{
		reason: "restart the guest shell",
	})

	receipt, err := runner.Apply(
		context.Background(),
		plan,
		plan.Digest,
		stateRoot,
	)
	if err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	if receipt.Complete || receipt.Outcomes[0].Status != "action-required" {
		t.Fatalf("receipt = %+v, want incomplete action-required", receipt)
	}
	paths, err := state.NewPaths(stateRoot, plan.Target.ID.String())
	if err != nil {
		t.Fatalf("NewPaths(): %v", err)
	}
	file, err := os.Open(paths.Journal)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("close journal: %v", err)
		}
	})
	var phases []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event state.JournalEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("decode journal event: %v", err)
		}
		phases = append(phases, event.Phase)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan journal: %v", err)
	}
	if got, want := phases, []string{"apply-started", "apply-action-required"}; !slices.Equal(got, want) {
		t.Fatalf("journal phases = %v, want %v", got, want)
	}
}

func TestStaleDigestMutatesNothing(t *testing.T) {
	plan := singleActionPlan(t, "lima-guest:mds")
	adapter := newFakeAdapter()
	stateRoot := filepath.Join(t.TempDir(), "state")

	_, err := testRunner(adapter).Apply(
		context.Background(),
		plan,
		"sha256:not-the-reviewed-plan",
		stateRoot,
	)
	if err == nil {
		t.Fatal("Apply() succeeded with stale digest")
	}
	if _, statErr := os.Stat(stateRoot); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("state root exists after stale preflight: %v", statErr)
	}
	if len(adapter.applyCount) != 0 {
		t.Fatalf("adapter mutated after stale preflight: %v", adapter.applyCount)
	}
}

func TestPlanWideComponentPreflightBlocksEveryMutationAndCleansRetainedInputs(t *testing.T) {
	plan := singleActionPlan(t, "lima-guest:mds")
	stateRoot := filepath.Join(t.TempDir(), "state")
	adapter := &planPreflightAdapter{
		fakeAdapter:  newFakeAdapter(),
		preflightErr: errors.New("child digest changed after approval"),
	}
	_, err := testRunner(adapter).Apply(
		context.Background(), plan, plan.Digest, stateRoot,
	)
	var stale *execution.StalePlanError
	if !errors.As(err, &stale) {
		t.Fatalf("Apply(preflight drift) error = %v, want stale plan", err)
	}
	if _, statErr := os.Lstat(stateRoot); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("state root exists after component preflight: %v", statErr)
	}
	if adapter.preflights != 1 || adapter.cleanups != 0 || len(adapter.applyCount) != 0 {
		t.Fatalf(
			"blocked lifecycle preflights=%d cleanups=%d applies=%v",
			adapter.preflights,
			adapter.cleanups,
			adapter.applyCount,
		)
	}

	adapter = &planPreflightAdapter{fakeAdapter: newFakeAdapter()}
	adapter.failOnce["a"] = true
	receipt, err := testRunner(adapter).Apply(
		context.Background(), plan, plan.Digest, stateRoot,
	)
	if err != nil {
		t.Fatalf("Apply(failed action): %v", err)
	}
	if receipt.Complete || adapter.preflights != 1 || adapter.cleanups != 1 {
		t.Fatalf(
			"failed lifecycle receipt=%+v preflights=%d cleanups=%d",
			receipt,
			adapter.preflights,
			adapter.cleanups,
		)
	}
}

func TestPlanWideActionRequiredPreflightPreservesClassification(t *testing.T) {
	plan := singleActionPlan(t, "lima-guest:mds")
	adapter := &planPreflightAdapter{
		fakeAdapter: newFakeAdapter(),
		preflightErr: &adapters.ActionRequiredError{
			Reason: "user-owned harness launcher must be resolved manually",
		},
	}
	stateRoot := filepath.Join(t.TempDir(), "state")
	_, err := testRunner(adapter).Apply(
		context.Background(), plan, plan.Digest, stateRoot,
	)
	var actionRequired *adapters.ActionRequiredError
	if !errors.As(err, &actionRequired) {
		t.Fatalf("Apply(action-required preflight) error = %v", err)
	}
	if _, statErr := os.Lstat(stateRoot); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("state root exists after action-required preflight: %v", statErr)
	}
}

func TestChangedTargetPreimageMutatesNothing(t *testing.T) {
	plan := singleActionPlan(t, "lima-guest:mds")
	adapter := newFakeAdapter()
	stateRoot := filepath.Join(t.TempDir(), "state")
	runner := testRunner(adapter)
	runner.ObserveTarget = func(
		_ context.Context,
		planned target.Facts,
	) (target.Facts, error) {
		planned.ImageRevision = "sha256:changed-after-review"
		return planned, nil
	}

	_, err := runner.Apply(context.Background(), plan, plan.Digest, stateRoot)
	if err == nil {
		t.Fatal("Apply() succeeded with changed target preimage")
	}
	if _, statErr := os.Stat(stateRoot); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("state root exists after preimage rejection: %v", statErr)
	}
	if len(adapter.applyCount) != 0 {
		t.Fatalf("adapter mutated after preimage rejection: %v", adapter.applyCount)
	}
}

func TestTargetStateIsIsolated(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	adapter := newFakeAdapter()
	runner := testRunner(adapter)
	for _, targetID := range []string{"lima-guest:mds", "wsl-guest:Ubuntu-26.04"} {
		plan := singleActionPlan(t, targetID)
		if _, err := runner.Apply(
			context.Background(),
			plan,
			plan.Digest,
			stateRoot,
		); err != nil {
			t.Fatalf("Apply(%s): %v", targetID, err)
		}
	}
	entries, err := os.ReadDir(stateRoot)
	if err != nil {
		t.Fatalf("ReadDir(state): %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("target state directories = %d, want 2", len(entries))
	}
}

func TestApplyEnforcesSingleWriterPerTarget(t *testing.T) {
	plan := singleActionPlan(t, "lima-guest:mds")
	stateRoot := filepath.Join(t.TempDir(), "state")
	paths, err := state.NewPaths(stateRoot, plan.Target.ID.String())
	if err != nil {
		t.Fatalf("NewPaths(): %v", err)
	}
	if err := paths.Ensure(); err != nil {
		t.Fatalf("Ensure(): %v", err)
	}
	lock, err := state.Acquire(paths.Lock)
	if err != nil {
		t.Fatalf("Acquire(first): %v", err)
	}
	t.Cleanup(func() {
		if err := lock.Release(); err != nil {
			t.Errorf("Release(first): %v", err)
		}
	})

	adapter := newFakeAdapter()
	_, err = testRunner(adapter).Apply(
		context.Background(),
		plan,
		plan.Digest,
		stateRoot,
	)
	if err == nil {
		t.Fatal("second writer acquired the same target lock")
	}
	if len(adapter.applyCount) != 0 {
		t.Fatalf("second writer mutated through adapter: %v", adapter.applyCount)
	}
}

func TestStateRejectsFilesystemRootSymlinkAndNonRegularFile(t *testing.T) {
	if _, err := state.NewPaths(string(filepath.Separator), "lima-guest:mds"); err == nil {
		t.Fatal("NewPaths() accepted filesystem root")
	}

	base := t.TempDir()
	realRoot := filepath.Join(base, "real")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatalf("mkdir real root: %v", err)
	}
	symlinkRoot := filepath.Join(base, "linked")
	if err := os.Symlink(realRoot, symlinkRoot); err != nil {
		t.Fatalf("symlink state root: %v", err)
	}
	paths, err := state.NewPaths(symlinkRoot, "lima-guest:mds")
	if err != nil {
		t.Fatalf("NewPaths(symlink): %v", err)
	}
	if err := paths.Ensure(); err == nil {
		t.Fatal("Ensure() accepted symlink state root")
	}

	normalPaths, err := state.NewPaths(filepath.Join(base, "state"), "lima-guest:mds")
	if err != nil {
		t.Fatalf("NewPaths(normal): %v", err)
	}
	if err := normalPaths.Ensure(); err != nil {
		t.Fatalf("Ensure(normal): %v", err)
	}
	if err := os.Mkdir(normalPaths.Journal, 0o700); err != nil {
		t.Fatalf("mkdir journal fixture: %v", err)
	}
	if err := normalPaths.Ensure(); err == nil {
		t.Fatal("Ensure() accepted directory at journal path")
	}
}

type fakeAdapter struct {
	mutex      sync.Mutex
	installed  map[string]bool
	applyCount map[string]int
	failOnce   map[string]bool
}

type planPreflightAdapter struct {
	*fakeAdapter
	preflightErr error
	preflights   int
	cleanups     int
}

func (adapter *planPreflightAdapter) Preflight(
	_ context.Context,
	_ planning.Plan,
) (func() error, error) {
	adapter.preflights++
	if adapter.preflightErr != nil {
		return nil, adapter.preflightErr
	}
	return func() error {
		adapter.cleanups++
		return nil
	}, nil
}

type actionRequiredAdapter struct {
	reason string
}

func (actionRequiredAdapter) Observe(
	context.Context,
	planning.Action,
) (adapters.Observation, error) {
	return adapters.Observation{State: adapters.StateAbsent}, nil
}

func (adapter actionRequiredAdapter) Apply(
	context.Context,
	planning.Action,
) error {
	return &adapters.ActionRequiredError{Reason: adapter.reason}
}

func (actionRequiredAdapter) Verify(context.Context, planning.Action) error {
	return nil
}

func newFakeAdapter() *fakeAdapter {
	return &fakeAdapter{
		installed:  make(map[string]bool),
		applyCount: make(map[string]int),
		failOnce:   make(map[string]bool),
	}
}

func (adapter *fakeAdapter) Observe(
	_ context.Context,
	action planning.Action,
) (adapters.Observation, error) {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	if adapter.installed[action.ComponentID] {
		return adapters.Observation{
			State: adapters.StateReady, InstalledVersion: action.Version,
		}, nil
	}
	return adapters.Observation{State: adapters.StateAbsent}, nil
}

func (adapter *fakeAdapter) Apply(
	_ context.Context,
	action planning.Action,
) error {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	adapter.applyCount[action.ComponentID]++
	if adapter.failOnce[action.ComponentID] {
		delete(adapter.failOnce, action.ComponentID)
		return fmt.Errorf("fixture failure for %s", action.ComponentID)
	}
	adapter.installed[action.ComponentID] = true
	return nil
}

func (adapter *fakeAdapter) Verify(
	_ context.Context,
	action planning.Action,
) error {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	if !adapter.installed[action.ComponentID] {
		return fmt.Errorf("%s is not installed", action.ComponentID)
	}
	return nil
}

func testRunner(adapter adapters.Component) execution.Runner {
	current := time.Date(2026, 7, 29, 6, 0, 0, 0, time.UTC)
	return execution.Runner{
		Adapter: adapter,
		ObserveTarget: func(
			_ context.Context,
			planned target.Facts,
		) (target.Facts, error) {
			return planned, nil
		},
		Now: func() time.Time {
			current = current.Add(time.Second)
			return current
		},
	}
}

func executionPlan(t *testing.T, targetID string) planning.Plan {
	t.Helper()
	id, err := target.ParseID(targetID)
	if err != nil {
		t.Fatalf("ParseID(): %v", err)
	}
	actions := []planning.Action{
		testAction(id, "a"),
		testAction(id, "b"),
		testAction(id, "c"),
		testAction(id, "d"),
	}
	actions[1].Dependencies = []string{actions[0].ID}
	actions[2].Dependencies = []string{actions[1].ID}
	plan := planning.Plan{
		SchemaVersion:   planning.PlanSchema,
		CatalogRevision: "sha256:catalog",
		Target: target.Facts{
			ID: id, OS: "linux", Architecture: "arm64", Reachable: true,
		},
		Selection: []string{"a", "b", "c", "d"},
		Actions:   actions,
		Blockers:  []planning.Blocker{},
	}
	digest, err := planning.Digest(plan)
	if err != nil {
		t.Fatalf("Digest(): %v", err)
	}
	plan.Digest = digest
	return plan
}

func singleActionPlan(t *testing.T, targetID string) planning.Plan {
	t.Helper()
	plan := executionPlan(t, targetID)
	plan.Selection = []string{"a"}
	plan.Actions = plan.Actions[:1]
	digest, err := planning.Digest(plan)
	if err != nil {
		t.Fatalf("Digest(single): %v", err)
	}
	plan.Digest = digest
	return plan
}

func testAction(id target.ID, component string) planning.Action {
	return planning.Action{
		ID:           id.String() + "/" + component,
		ComponentID:  component,
		TargetID:     id.String(),
		Status:       planning.ActionPlanned,
		Installer:    "fixture",
		Package:      component,
		Version:      "1.0.0",
		Dependencies: []string{},
		Verification: [][]string{{component, "--version"}},
	}
}

func outcomesByComponent(receipt state.Receipt) map[string]state.ActionOutcome {
	result := make(map[string]state.ActionOutcome, len(receipt.Outcomes))
	for _, outcome := range receipt.Outcomes {
		component := filepath.Base(outcome.ActionID)
		result[component] = outcome
	}
	return result
}

func assertStatus(
	t *testing.T,
	outcomes map[string]state.ActionOutcome,
	component,
	want string,
) {
	t.Helper()
	if got := outcomes[component].Status; got != want {
		t.Fatalf("status %s = %q, want %q", component, got, want)
	}
}
