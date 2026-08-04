package planning

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/harness"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
)

type fakeSnapshot struct {
	id       string
	root     string
	exec     string
	closed   *[]string
	closeErr error
}

func (snapshot *fakeSnapshot) Root() string       { return snapshot.root }
func (snapshot *fakeSnapshot) Executable() string { return snapshot.exec }
func (snapshot *fakeSnapshot) Path(relative string) string {
	return snapshot.root + "/" + relative
}
func (snapshot *fakeSnapshot) Close() error {
	*snapshot.closed = append(*snapshot.closed, snapshot.id)
	return snapshot.closeErr
}

type fakeAcquirer struct {
	requests []artifact.SnapshotRequest
	closed   []string
	failAt   int
	closeAt  int
}

func (acquirer *fakeAcquirer) Acquire(
	_ context.Context,
	request artifact.SnapshotRequest,
) (VerifiedSnapshot, error) {
	acquirer.requests = append(acquirer.requests, request)
	index := len(acquirer.requests)
	if acquirer.failAt == index {
		return nil, errors.New("token=must-not-leak /private/acquire")
	}
	id := request.Alias
	if id == "" {
		id = "omh"
		if strings.Contains(request.URL, "omh-node-runtime") {
			id = "node"
		}
	}
	return &fakeSnapshot{
		id: id, root: "/private/snapshots/" + id,
		exec:   "/private/snapshots/" + id + "/executable",
		closed: &acquirer.closed,
		closeErr: func() error {
			if acquirer.closeAt == index {
				return errors.New("/private/cleanup token=must-not-leak")
			}
			return nil
		}(),
	}, nil
}

type fakePreviewer struct {
	requests []harness.Request
	result   harness.Result
	err      error
}

func (previewer *fakePreviewer) Preview(
	_ context.Context,
	request harness.Request,
) (harness.Result, error) {
	previewer.requests = append(previewer.requests, request)
	return previewer.result, previewer.err
}

func TestComposerAcquiresSelectedArtifactsAndComposesDeterministically(t *testing.T) {
	environment, base := compositionFixture(t, target.KindMacOSHost, []string{
		"opencode", "oh-my-harness", "claude-code", "codex", "omh-node-runtime",
	})
	acquirer := &fakeAcquirer{}
	previewer := &fakePreviewer{result: childPreview([]string{
		"claude-code", "codex", "opencode",
	}, "a")}
	composer := testComposer(acquirer, previewer)

	first, err := composer.Compose(context.Background(), environment, base)
	if err != nil {
		t.Fatalf("Compose(first): %v", err)
	}
	firstRequests := append([]artifact.SnapshotRequest(nil), acquirer.requests...)
	acquirer.requests = nil
	acquirer.closed = nil
	second, err := composer.Compose(context.Background(), environment, base)
	if err != nil {
		t.Fatalf("Compose(second): %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("composition is not deterministic:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if len(firstRequests) != 5 {
		t.Fatalf("acquisitions = %d, want node+omh+3 selected agents", len(firstRequests))
	}
	aliases := make([]string, 0, len(firstRequests))
	for _, request := range firstRequests {
		aliases = append(aliases, request.Alias)
	}
	if want := []string{"", "", "claude", "codex", "opencode"}; !reflect.DeepEqual(aliases, want) {
		t.Fatalf("artifact aliases = %q, want %q", aliases, want)
	}
	if firstRequests[0].ExtractAll ||
		!strings.HasSuffix(firstRequests[0].Executable, "/bin/node") {
		t.Fatalf("Node request = %+v, want original archive executable path", firstRequests[0])
	}
	if !firstRequests[1].ExtractAll ||
		firstRequests[1].Executable != "package/omh" {
		t.Fatalf("OMH request = %+v, want full extraction", firstRequests[1])
	}
	if got := previewer.requests[0].Entrypoint; !strings.HasSuffix(got, "/package/dist/cli/main.js") {
		t.Fatalf("entrypoint = %q", got)
	}
	inputs := actionByComponent(t, first, "oh-my-harness").Inputs
	for key, want := range map[string]string{
		"harness_child_digest":    strings.Repeat("a", 64),
		"harness_selected_agents": "claude-code,codex,opencode",
		"harness_workflows":       strings.Join(exactCompositionWorkflows, ","),
	} {
		if inputs[key] != want {
			t.Fatalf("harness input %s = %q, want %q", key, inputs[key], want)
		}
	}
	if first.Digest == base.Digest {
		t.Fatal("outer digest did not change after composition")
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("Marshal(composed): %v", err)
	}
	for _, forbidden := range []string{
		"/private/", composer.Home, composer.StateRoot, "must-not-leak",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("composed plan leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestComposerEmptySelectionIsStableAndAcquiresNoAgent(t *testing.T) {
	environment, base := compositionFixture(t, target.KindMacOSHost, []string{
		"omh-node-runtime", "oh-my-harness",
	})
	acquirer := &fakeAcquirer{}
	previewer := &fakePreviewer{result: childPreview(nil, "a")}
	composed, err := testComposer(acquirer, previewer).Compose(
		context.Background(), environment, base,
	)
	if err != nil {
		t.Fatalf("Compose(): %v", err)
	}
	if len(acquirer.requests) != 2 || len(previewer.requests) != 1 ||
		len(previewer.requests[0].AgentExecutables) != 0 {
		t.Fatalf("empty selection acquired agents: requests=%+v preview=%+v", acquirer.requests, previewer.requests)
	}
	inputs := actionByComponent(t, composed, "oh-my-harness").Inputs
	if inputs["harness_selected_agents"] != "none" ||
		inputs["harness_agent_identities"] != "none" {
		t.Fatalf("empty identities = %+v", inputs)
	}
}

func TestComposerSkipsNoHarnessAndGuestPlansByteForByte(t *testing.T) {
	for _, test := range []struct {
		name string
		kind target.Kind
		ids  []string
	}{
		{name: "no harness", kind: target.KindMacOSHost, ids: []string{"codex"}},
		{name: "guest", kind: target.KindLimaGuest, ids: []string{"omh-node-runtime", "oh-my-harness"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			environment, base := compositionFixture(t, test.kind, test.ids)
			before, err := json.Marshal(base)
			if err != nil {
				t.Fatalf("Marshal(base): %v", err)
			}
			acquirer := &fakeAcquirer{}
			previewer := &fakePreviewer{}
			got, err := testComposer(acquirer, previewer).Compose(
				context.Background(), environment, base,
			)
			if err != nil {
				t.Fatalf("Compose(): %v", err)
			}
			after, _ := json.Marshal(got)
			if string(before) != string(after) || len(acquirer.requests) != 0 ||
				len(previewer.requests) != 0 {
				t.Fatalf("skipped plan changed/acquired: before=%s after=%s", before, after)
			}
		})
	}
}

func TestComposeBindsChildConfigArtifactAndBasePreimages(t *testing.T) {
	environment, base := compositionFixture(t, target.KindMacOSHost, []string{
		"omh-node-runtime", "oh-my-harness", "codex",
	})
	composeWith := func(child harness.Result, env catalog.Environment, plan Plan) Plan {
		t.Helper()
		got, err := testComposer(&fakeAcquirer{}, &fakePreviewer{result: child}).Compose(
			context.Background(), env, plan,
		)
		if err != nil {
			t.Fatalf("Compose(): %v", err)
		}
		return got
	}
	first := composeWith(childPreview([]string{"codex"}, "a"), environment, base)
	changedChild := composeWith(childPreview([]string{"codex"}, "d"), environment, base)
	changedConfigChild := childPreview([]string{"codex"}, "a")
	changedConfigChild.ConfigDigest = "sha256:" + strings.Repeat("e", 64)
	changedConfig := composeWith(changedConfigChild, environment, base)
	changedEnvironment := environment
	changedEnvironment.Lock.Versions = cloneLocks(environment.Lock.Versions)
	entry := changedEnvironment.Lock.Versions["codex"]
	entry.Artifacts = cloneArtifacts(entry.Artifacts)
	artifactValue := entry.Artifacts["darwin-arm64"]
	artifactValue.ExecutableSHA256 = strings.Repeat("f", 64)
	entry.Artifacts["darwin-arm64"] = artifactValue
	changedEnvironment.Lock.Versions["codex"] = entry
	changedArtifact := composeWith(childPreview([]string{"codex"}, "a"), changedEnvironment, base)
	changedBase := base
	changedBase.Actions = append([]Action(nil), base.Actions...)
	changedBase.Actions[0].Inputs = map[string]string{"preimage": "changed"}
	changedBase.Digest, _ = Digest(changedBase)
	changedPreimage := composeWith(childPreview([]string{"codex"}, "a"), environment, changedBase)
	for label, got := range map[string]string{
		"child": changedChild.Digest, "config": changedConfig.Digest,
		"artifact": changedArtifact.Digest, "preimage": changedPreimage.Digest,
	} {
		if got == first.Digest {
			t.Fatalf("%s change did not alter outer digest %s", label, got)
		}
	}
}

func TestComposerCleansSnapshotsInReverseOnSuccessAndFailure(t *testing.T) {
	environment, base := compositionFixture(t, target.KindMacOSHost, []string{
		"omh-node-runtime", "oh-my-harness", "codex",
	})
	for _, test := range []struct {
		name      string
		failAt    int
		closeAt   int
		wantClose []string
		wantReady bool
	}{
		{name: "success", wantClose: []string{"codex", "omh", "node"}, wantReady: true},
		{name: "acquisition failure", failAt: 3, wantClose: []string{"omh", "node"}},
		{name: "cleanup failure", closeAt: 2, wantClose: []string{"codex", "omh", "node"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			acquirer := &fakeAcquirer{failAt: test.failAt, closeAt: test.closeAt}
			got, err := testComposer(acquirer, &fakePreviewer{
				result: childPreview([]string{"codex"}, "a"),
			}).Compose(context.Background(), environment, base)
			if err != nil {
				t.Fatalf("Compose(): %v", err)
			}
			if !reflect.DeepEqual(acquirer.closed, test.wantClose) {
				t.Fatalf("cleanup order = %q, want %q", acquirer.closed, test.wantClose)
			}
			action := actionByComponent(t, got, "oh-my-harness")
			if test.wantReady && action.Status != ActionPlanned {
				t.Fatalf("successful action = %+v", action)
			}
			if !test.wantReady && action.Status != ActionActionRequired {
				t.Fatalf("failed action = %+v, want action-required", action)
			}
		})
	}
}

func TestComposerSanitizesChildBlockedAndErrors(t *testing.T) {
	environment, base := compositionFixture(t, target.KindMacOSHost, []string{
		"omh-node-runtime", "oh-my-harness", "codex",
	})
	tests := []struct {
		name   string
		result harness.Result
		err    error
	}{
		{name: "blocked", result: func() harness.Result {
			value := childPreview([]string{"codex"}, "a")
			value.Readiness = "blocked"
			value.Digest = ""
			value.Blockers = []string{"native:codex"}
			return value
		}()},
		{name: "timeout", err: &harness.Error{Code: "timeout"}},
		{name: "raw error", err: errors.New("token=must-not-leak /Users/private")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := testComposer(&fakeAcquirer{}, &fakePreviewer{
				result: test.result, err: test.err,
			}).Compose(context.Background(), environment, base)
			if err != nil {
				t.Fatalf("Compose(): %v", err)
			}
			encoded, _ := json.Marshal(got)
			if actionByComponent(t, got, "oh-my-harness").Status != ActionActionRequired {
				t.Fatalf("composition failure did not block: %s", encoded)
			}
			for _, forbidden := range []string{"must-not-leak", "/Users/", "/private/", "token="} {
				if strings.Contains(string(encoded), forbidden) {
					t.Fatalf("blocked plan leaked %q: %s", forbidden, encoded)
				}
			}
		})
	}
}

func TestComposerBlocksMissingOrWrongPlatformLockWithoutAcquisition(t *testing.T) {
	environment, base := compositionFixture(t, target.KindMacOSHost, []string{
		"omh-node-runtime", "oh-my-harness", "codex",
	})
	for _, mutate := range []func(*catalog.Environment){
		func(environment *catalog.Environment) {
			entry := environment.Lock.Versions["codex"]
			entry.Artifacts = map[string]catalog.Artifact{}
			environment.Lock.Versions["codex"] = entry
		},
		func(environment *catalog.Environment) {
			entry := environment.Lock.Versions["codex"]
			entry.Artifacts = cloneArtifacts(entry.Artifacts)
			value := entry.Artifacts["darwin-arm64"]
			value.ExecutableSHA256 = ""
			entry.Artifacts["darwin-arm64"] = value
			environment.Lock.Versions["codex"] = entry
		},
	} {
		changed := environment
		changed.Lock.Versions = cloneLocks(environment.Lock.Versions)
		mutate(&changed)
		acquirer := &fakeAcquirer{}
		got, err := testComposer(acquirer, &fakePreviewer{}).Compose(
			context.Background(), changed, base,
		)
		if err != nil {
			t.Fatalf("Compose(): %v", err)
		}
		if len(acquirer.requests) != 0 ||
			actionByComponent(t, got, "oh-my-harness").Status != ActionActionRequired {
			t.Fatalf("invalid lock did not fail before acquisition: %+v", got)
		}
	}
}

func TestComposerBlocksUnsafeArtifactURLBeforeAnyAcquisition(t *testing.T) {
	environment, base := compositionFixture(t, target.KindMacOSHost, []string{
		"omh-node-runtime", "oh-my-harness", "codex",
	})
	entry := environment.Lock.Versions["codex"]
	entry.Artifacts = cloneArtifacts(entry.Artifacts)
	value := entry.Artifacts["darwin-arm64"]
	value.URL = "http://example.test/codex.tgz?token=must-not-leak"
	entry.Artifacts["darwin-arm64"] = value
	environment.Lock.Versions["codex"] = entry
	acquirer := &fakeAcquirer{}

	got, err := testComposer(acquirer, &fakePreviewer{}).Compose(
		context.Background(), environment, base,
	)
	if err != nil {
		t.Fatalf("Compose(): %v", err)
	}
	if len(acquirer.requests) != 0 ||
		actionByComponent(t, got, "oh-my-harness").Status != ActionActionRequired {
		t.Fatalf("unsafe URL did not fail before acquisition: %+v", got)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "must-not-leak") ||
		strings.Contains(string(encoded), "http://") {
		t.Fatalf("unsafe URL leaked into blocked plan: %s", encoded)
	}
}

func TestComposerBlocksStaleBaseBeforeAnyAcquisition(t *testing.T) {
	environment, base := compositionFixture(t, target.KindMacOSHost, []string{
		"omh-node-runtime", "oh-my-harness",
	})
	base.Digest = strings.Repeat("0", 64)
	acquirer := &fakeAcquirer{}

	got, err := testComposer(acquirer, &fakePreviewer{}).Compose(
		context.Background(), environment, base,
	)
	if err != nil {
		t.Fatalf("Compose(): %v", err)
	}
	if len(acquirer.requests) != 0 ||
		actionByComponent(t, got, "oh-my-harness").Status != ActionActionRequired {
		t.Fatalf("stale base did not fail before acquisition: %+v", got)
	}
}

func TestComposerSurfacesOnlySafeChildBlockerIDs(t *testing.T) {
	environment, base := compositionFixture(t, target.KindMacOSHost, []string{
		"omh-node-runtime", "oh-my-harness", "codex",
	})
	child := childPreview([]string{"codex"}, "a")
	child.Readiness = "blocked"
	child.Digest = ""
	child.Blockers = []string{"native-registration:codex"}
	got, err := testComposer(&fakeAcquirer{}, &fakePreviewer{result: child}).Compose(
		context.Background(), environment, base,
	)
	if err != nil {
		t.Fatalf("Compose(): %v", err)
	}
	reason := actionByComponent(t, got, "oh-my-harness").Reason
	if !strings.Contains(reason, "native-registration:codex") {
		t.Fatalf("safe child blocker omitted from reason: %q", reason)
	}

	child.Blockers = []string{"token=must-not-leak", "/Users/private"}
	blocked, err := testComposer(&fakeAcquirer{}, &fakePreviewer{result: child}).Compose(
		context.Background(), environment, base,
	)
	if err != nil {
		t.Fatalf("Compose(unsafe blockers): %v", err)
	}
	encoded, _ := json.Marshal(blocked)
	for _, forbidden := range []string{"must-not-leak", "/Users/", "token="} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("unsafe child blocker leaked %q: %s", forbidden, encoded)
		}
	}
}

func testComposer(acquirer SnapshotAcquirer, previewer ChildPreviewer) Composer {
	return Composer{
		Acquirer: acquirer, Previewer: previewer,
		Home: "/private/home", ConfigRoot: "/private/home/.config",
		TempRoot: "/private/tmp", StateRoot: "/private/home/.local/state/omh",
		Locale: "C.UTF-8",
	}
}

func childPreview(agents []string, digestCharacter string) harness.Result {
	addons := make([]harness.Addon, 0, 2)
	ownership := make([]harness.Ownership, 0, len(agents))
	for _, id := range agents {
		ownership = append(ownership, harness.Ownership{
			ID: id, Ownership: "external", State: "ready",
		})
		if id == "opencode" || id == "codex" {
			addons = append(addons, harness.Addon{
				AgentID: id, ID: "omo", Version: "4.19.2", State: "installable",
				Fingerprint: strings.Repeat("c", 64),
			})
		}
	}
	return harness.Result{
		SchemaVersion: "2.0.0", Digest: strings.Repeat(digestCharacter, 64),
		CatalogRevision: strings.Repeat("b", 64), Readiness: "preview",
		SelectedAgents: append([]string(nil), agents...),
		Workflows:      append([]string(nil), exactCompositionWorkflows...),
		Addons:         addons, Ownership: ownership,
		ConfigDigest: "sha256:" + strings.Repeat("c", 64),
		Blockers:     []string{}, OptionalGapIDs: []string{},
	}
}

func compositionFixture(
	t *testing.T,
	kind target.Kind,
	ids []string,
) (catalog.Environment, Plan) {
	t.Helper()
	platform := "darwin-arm64"
	osName := "darwin"
	if kind == target.KindWindowsHost {
		platform = "windows-arm64"
		osName = "windows"
	}
	locks := make(map[string]catalog.LockEntry)
	components := make([]catalog.Component, 0, 5)
	for _, id := range []string{
		"omh-node-runtime", "oh-my-harness", "claude-code", "codex", "opencode",
	} {
		executable := id
		if id == "omh-node-runtime" {
			executable = "node-v22/bin/node"
		}
		if id == "oh-my-harness" {
			executable = "package/omh"
		}
		if id == "claude-code" {
			executable = "claude"
		}
		locks[id] = catalog.LockEntry{
			Version: "1.0.0", Source: "fixture", Provenance: "https://example.test/" + id,
			Artifacts: map[string]catalog.Artifact{
				platform: {
					URL:    "https://example.test/" + id + ".tgz",
					SHA256: strings.Repeat(string('1'+rune(len(id)%8)), 64),
					Format: "tar.gz", Executable: executable,
					ExecutableSHA256: func() string {
						if id == "claude-code" || id == "codex" || id == "opencode" {
							return strings.Repeat("e", 64)
						}
						return ""
					}(),
				},
			},
		}
		components = append(components, catalog.Component{
			ID: id, VersionPolicy: catalog.VersionPolicy{Mode: "pinned", LockKey: id},
		})
	}
	environment := catalog.Environment{
		Catalog: catalog.Catalog{SchemaVersion: 1, Components: components},
		Lock:    catalog.VersionLock{SchemaVersion: 1, Versions: locks},
	}
	targetName := "fixture"
	if kind == target.KindMacOSHost || kind == target.KindWindowsHost {
		targetName = "local"
	}
	targetID, err := target.NewID(kind, targetName)
	if err != nil {
		t.Fatalf("NewID(): %v", err)
	}
	base := Plan{
		SchemaVersion: PlanSchema, CatalogRevision: strings.Repeat("9", 64),
		Target:    target.Facts{ID: targetID, OS: osName, Architecture: "arm64"},
		Selection: append([]string(nil), ids...), Actions: make([]Action, 0, len(ids)),
		Blockers: []Blocker{},
	}
	for _, id := range ids {
		base.Actions = append(base.Actions, Action{
			ID: targetID.String() + "/" + id, ComponentID: id,
			TargetID: targetID.String(), Status: ActionPlanned,
			Version: locks[id].Version, Dependencies: []string{}, Verification: [][]string{},
		})
	}
	base.Digest, err = Digest(base)
	if err != nil {
		t.Fatalf("Digest(base): %v", err)
	}
	return environment, base
}

func actionByComponent(t *testing.T, plan Plan, id string) Action {
	t.Helper()
	for _, action := range plan.Actions {
		if action.ComponentID == id {
			return action
		}
	}
	t.Fatalf("plan has no action for %s", id)
	return Action{}
}

func cloneLocks(source map[string]catalog.LockEntry) map[string]catalog.LockEntry {
	result := make(map[string]catalog.LockEntry, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneArtifacts(source map[string]catalog.Artifact) map[string]catalog.Artifact {
	result := make(map[string]catalog.Artifact, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
