package integration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/state"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	updateflow "github.com/zzanghyunmoo/my-desk-setup/internal/update"
)

func TestUpdateLosingToApplyMutatesNeitherCatalogNorTarget(t *testing.T) {
	catalogRoot := copyEmbeddedCatalog(t)
	plan, beforeVersion := concurrencyUpdatePlan(t, catalogRoot)
	stateRoot := t.TempDir()

	applyAdapter := newBlockingAdapter()
	applyResult := make(chan error, 1)
	go func() {
		_, err := testRunner(applyAdapter).Apply(
			context.Background(),
			plan.TargetPlan,
			plan.TargetPlan.Digest,
			stateRoot,
		)
		applyResult <- err
	}()
	t.Cleanup(applyAdapter.release)
	applyAdapter.waitUntilBlocked(t)

	updateAdapter := newFakeAdapter()
	_, updateErr := updateflow.Apply(
		context.Background(),
		plan,
		plan.Digest,
		catalogRoot,
		stateRoot,
		testRunner(updateAdapter),
	)
	afterVersion := catalogVersion(t, catalogRoot, plan.ComponentID)
	updateMutations := totalApplyCount(updateAdapter)

	applyAdapter.release()
	if err := waitForConcurrentResult(t, applyResult); err != nil {
		t.Fatalf("winning apply failed: %v", err)
	}
	if updateErr == nil {
		t.Error("losing update succeeded while apply held the target lease")
	} else if !errors.Is(updateErr, state.ErrLockContended) {
		t.Errorf("losing update error = %v, want target contention", updateErr)
	}
	if afterVersion != beforeVersion {
		t.Errorf(
			"losing update changed catalog version: got=%q want=%q",
			afterVersion,
			beforeVersion,
		)
	}
	if updateMutations != 0 {
		t.Errorf("losing update mutated target %d time(s)", updateMutations)
	}
}

func TestLosingConcurrentUpdateMutatesNeitherCatalogNorTarget(t *testing.T) {
	catalogRoot := copyEmbeddedCatalog(t)
	plan, beforeVersion := concurrencyUpdatePlan(t, catalogRoot)
	stateRoot := t.TempDir()

	winnerAdapter := newFakeAdapter()
	winnerRunner := testRunner(winnerAdapter)
	barrier := newTargetObservationBarrier(winnerRunner.ObserveTarget)
	winnerRunner.ObserveTarget = barrier.observe
	winnerResult := make(chan error, 1)
	go func() {
		_, err := updateflow.Apply(
			context.Background(),
			plan,
			plan.Digest,
			catalogRoot,
			stateRoot,
			winnerRunner,
		)
		winnerResult <- err
	}()
	t.Cleanup(barrier.release)
	barrier.waitUntilBlocked(t)

	loserAdapter := newFakeAdapter()
	_, loserErr := updateflow.Apply(
		context.Background(),
		plan,
		plan.Digest,
		catalogRoot,
		stateRoot,
		testRunner(loserAdapter),
	)
	afterVersion := catalogVersion(t, catalogRoot, plan.ComponentID)
	loserMutations := totalApplyCount(loserAdapter)

	barrier.release()
	if err := waitForConcurrentResult(t, winnerResult); err != nil {
		t.Fatalf("winning update failed: %v", err)
	}
	if loserErr == nil {
		t.Error("losing concurrent update succeeded")
	} else if !errors.Is(loserErr, state.ErrLockContended) {
		t.Errorf("losing update error = %v, want catalog contention", loserErr)
	}
	if afterVersion != beforeVersion {
		t.Errorf(
			"losing update changed catalog version: got=%q want=%q",
			afterVersion,
			beforeVersion,
		)
	}
	if loserMutations != 0 {
		t.Errorf("losing update mutated target %d time(s)", loserMutations)
	}
}

type blockingAdapter struct {
	delegate      *fakeAdapter
	entered       chan struct{}
	continueOnce  sync.Once
	releaseSignal chan struct{}
	blockOnce     sync.Once
}

func newBlockingAdapter() *blockingAdapter {
	return &blockingAdapter{
		delegate:      newFakeAdapter(),
		entered:       make(chan struct{}),
		releaseSignal: make(chan struct{}),
	}
}

func (adapter *blockingAdapter) Observe(
	ctx context.Context,
	action planning.Action,
) (adapters.Observation, error) {
	adapter.blockOnce.Do(func() {
		close(adapter.entered)
		select {
		case <-adapter.releaseSignal:
		case <-ctx.Done():
		}
	})
	return adapter.delegate.Observe(ctx, action)
}

func (adapter *blockingAdapter) Apply(
	ctx context.Context,
	action planning.Action,
) error {
	return adapter.delegate.Apply(ctx, action)
}

func (adapter *blockingAdapter) Verify(
	ctx context.Context,
	action planning.Action,
) error {
	return adapter.delegate.Verify(ctx, action)
}

func (adapter *blockingAdapter) waitUntilBlocked(t *testing.T) {
	t.Helper()
	waitForBarrier(t, adapter.entered)
}

func (adapter *blockingAdapter) release() {
	adapter.continueOnce.Do(func() {
		close(adapter.releaseSignal)
	})
}

type targetObservationBarrier struct {
	delegate      func(context.Context, target.Facts) (target.Facts, error)
	entered       chan struct{}
	releaseSignal chan struct{}
	blockOnce     sync.Once
	continueOnce  sync.Once
}

func newTargetObservationBarrier(
	delegate func(context.Context, target.Facts) (target.Facts, error),
) *targetObservationBarrier {
	return &targetObservationBarrier{
		delegate: delegate, entered: make(chan struct{}),
		releaseSignal: make(chan struct{}),
	}
}

func (barrier *targetObservationBarrier) observe(
	ctx context.Context,
	facts target.Facts,
) (target.Facts, error) {
	barrier.blockOnce.Do(func() {
		close(barrier.entered)
		select {
		case <-barrier.releaseSignal:
		case <-ctx.Done():
		}
	})
	return barrier.delegate(ctx, facts)
}

func (barrier *targetObservationBarrier) waitUntilBlocked(t *testing.T) {
	t.Helper()
	waitForBarrier(t, barrier.entered)
}

func (barrier *targetObservationBarrier) release() {
	barrier.continueOnce.Do(func() {
		close(barrier.releaseSignal)
	})
}

func concurrencyUpdatePlan(
	t *testing.T,
	catalogRoot string,
) (updateflow.Plan, string) {
	t.Helper()
	environment, err := catalog.Load(catalogRoot)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	beforeVersion := environment.Lock.Versions["typescript"].Version
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
	return plan, beforeVersion
}

func catalogVersion(t *testing.T, root, component string) string {
	t.Helper()
	environment, err := catalog.Load(root)
	if err != nil {
		t.Fatalf("Load(%s): %v", root, err)
	}
	return environment.Lock.Versions[component].Version
}

func totalApplyCount(adapter *fakeAdapter) int {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	total := 0
	for _, count := range adapter.applyCount {
		total += count
	}
	return total
}

func waitForBarrier(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for concurrency barrier")
	}
}

func waitForConcurrentResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for concurrent operation")
		return nil
	}
}

var _ adapters.Component = (*blockingAdapter)(nil)
