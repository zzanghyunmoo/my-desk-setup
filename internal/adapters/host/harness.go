package host

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/managedfile"
	"github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/durable"
	harnessruntime "github.com/zzanghyunmoo/my-desk-setup/internal/harness"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

const (
	harnessMarkerSchema = "mds.host-harness/v1"
	harnessMarkerName   = ".mds-harness.json"
	maxHarnessEntries   = 30_000
	maxHarnessBytes     = int64(1 << 30)
)

// HarnessRuntime is the narrow child-process seam used by the host harness.
// Production uses harness.Runner; tests can prove lifecycle ordering without
// invoking an agent or authentication command.
type HarnessRuntime interface {
	Preview(context.Context, harnessruntime.Request) (harnessruntime.Result, error)
	Apply(context.Context, harnessruntime.Request, string) (harnessruntime.ApplyResult, error)
}

// Harness owns the host-only OMH lifecycle. The release and agent snapshots
// are acquired during a plan-wide preflight and retained until the complete
// outer apply finishes, preventing mutation between approval and execution.
type Harness struct {
	Environment catalog.Environment
	Composer    planning.Composer
	Runtime     HarnessRuntime
	Home        string
	Platform    string

	mu      sync.Mutex
	session *harnessSession
}

type harnessSession struct {
	actionID     string
	actionDigest string
	request      harnessruntime.Request
	sourceRoot   string
	snapshots    []planning.VerifiedSnapshot
	preimages    []harnessSnapshotPreimage
	cleanupOnce  sync.Once
	cleanupErr   error
}

type harnessSnapshotPreimage struct {
	root   string
	digest string
}

type retainingAcquirer struct {
	delegate        planning.SnapshotAcquirer
	snapshots       []planning.VerifiedSnapshot
	harnessSnapshot planning.VerifiedSnapshot
}

type retainedSnapshot struct {
	planning.VerifiedSnapshot
}

func (retainedSnapshot) Close() error { return nil }

func (acquirer *retainingAcquirer) Acquire(
	ctx context.Context,
	request artifact.SnapshotRequest,
) (planning.VerifiedSnapshot, error) {
	snapshot, err := acquirer.delegate.Acquire(ctx, request)
	if err != nil || snapshot == nil {
		return nil, err
	}
	acquirer.snapshots = append(acquirer.snapshots, snapshot)
	if request.ExtractAll {
		if acquirer.harnessSnapshot != nil {
			return nil, errors.New("multiple harness payload snapshots acquired")
		}
		acquirer.harnessSnapshot = snapshot
	}
	return retainedSnapshot{VerifiedSnapshot: snapshot}, nil
}

type capturingPreviewer struct {
	delegate HarnessRuntime
	requests []harnessruntime.Request
}

func (previewer *capturingPreviewer) Preview(
	ctx context.Context,
	request harnessruntime.Request,
) (harnessruntime.Result, error) {
	previewer.requests = append(previewer.requests, request)
	return previewer.delegate.Preview(ctx, request)
}

type harnessGenerationMarker struct {
	Schema        string `json:"schema"`
	Version       string `json:"version"`
	ArchiveSHA256 string `json:"archive_sha256"`
	TreeSHA256    string `json:"tree_sha256"`
}

func (harness *Harness) Preflight(
	ctx context.Context,
	plan planning.Plan,
) (func() error, error) {
	if err := harness.validate(); err != nil {
		return nil, err
	}
	action, err := harnessAction(plan)
	if err != nil {
		return nil, err
	}
	if action.Status != planning.ActionPlanned {
		return nil, &adapters.ActionRequiredError{Reason: action.Reason}
	}
	if err := validateHarnessAction(action); err != nil {
		return nil, err
	}
	if err := harness.validateDestination(action); err != nil {
		return nil, &adapters.ActionRequiredError{Reason: err.Error()}
	}

	acquirer := &retainingAcquirer{delegate: harness.Composer.Acquirer}
	previewer := &capturingPreviewer{delegate: harness.Runtime}
	composer := harness.Composer
	composer.Acquirer = acquirer
	composer.Previewer = previewer
	recomposed, composeErr := composer.Compose(ctx, harness.Environment, plan)
	if composeErr != nil {
		return nil, errors.Join(composeErr, closeSnapshots(acquirer.snapshots))
	}
	if recomposed.Digest != plan.Digest || len(previewer.requests) != 1 ||
		acquirer.harnessSnapshot == nil {
		return nil, errors.Join(
			errors.New("approved host harness plan no longer recomposes exactly"),
			closeSnapshots(acquirer.snapshots),
		)
	}
	request := previewer.requests[0]
	if action.Inputs["harness_child_digest"] == "" ||
		action.Inputs["harness_child_digest"] != recomposedActionInput(
			recomposed, action.ComponentID, "harness_child_digest",
		) {
		return nil, errors.Join(
			errors.New("approved child digest is missing or stale"),
			closeSnapshots(acquirer.snapshots),
		)
	}
	sourceRoot := filepath.Clean(acquirer.harnessSnapshot.Root())
	if !filepath.IsAbs(sourceRoot) {
		return nil, errors.Join(
			errors.New("verified harness snapshot root is not absolute"),
			closeSnapshots(acquirer.snapshots),
		)
	}
	entrypoint, err := filepath.Abs(request.Entrypoint)
	if err != nil || !pathWithin(sourceRoot, entrypoint) ||
		filepath.Clean(filepath.Join(sourceRoot, "package", "dist", "cli", "main.js")) != entrypoint {
		return nil, errors.Join(
			errors.New("verified harness entrypoint escaped its retained snapshot"),
			closeSnapshots(acquirer.snapshots),
		)
	}

	actionDigest, err := digestHarnessAction(action)
	if err != nil {
		return nil, errors.Join(err, closeSnapshots(acquirer.snapshots))
	}
	preimages, err := captureHarnessSnapshotPreimages(acquirer.snapshots)
	if err != nil {
		return nil, errors.Join(err, closeSnapshots(acquirer.snapshots))
	}
	session := &harnessSession{
		actionID: action.ID, actionDigest: actionDigest,
		request:    request,
		sourceRoot: sourceRoot, snapshots: acquirer.snapshots, preimages: preimages,
	}
	harness.mu.Lock()
	if harness.session != nil {
		harness.mu.Unlock()
		return nil, errors.Join(
			errors.New("host harness preflight session already exists"),
			session.cleanup(),
		)
	}
	harness.session = session
	harness.mu.Unlock()
	return func() error {
		harness.mu.Lock()
		if harness.session == session {
			harness.session = nil
		}
		harness.mu.Unlock()
		return session.cleanup()
	}, nil
}

func (session *harnessSession) cleanup() error {
	session.cleanupOnce.Do(func() {
		session.cleanupErr = closeSnapshots(session.snapshots)
	})
	return session.cleanupErr
}

func closeSnapshots(snapshots []planning.VerifiedSnapshot) error {
	var result error
	for index := len(snapshots) - 1; index >= 0; index-- {
		result = errors.Join(result, snapshots[index].Close())
	}
	return result
}

func captureHarnessSnapshotPreimages(
	snapshots []planning.VerifiedSnapshot,
) ([]harnessSnapshotPreimage, error) {
	result := make([]harnessSnapshotPreimage, 0, len(snapshots))
	for _, snapshot := range snapshots {
		root := filepath.Clean(snapshot.Root())
		if !filepath.IsAbs(root) {
			return nil, errors.New("verified snapshot root is not absolute")
		}
		digest, _, _, err := harnessTreeDigest(root, "")
		if err != nil {
			return nil, fmt.Errorf("capture verified snapshot preimage: %w", err)
		}
		result = append(result, harnessSnapshotPreimage{root: root, digest: digest})
	}
	return result, nil
}

func (session *harnessSession) verifySnapshotPreimages() error {
	for _, preimage := range session.preimages {
		digest, _, _, err := harnessTreeDigest(preimage.root, "")
		if err != nil || digest != preimage.digest {
			return errors.New("verified harness input changed after plan-wide preflight")
		}
	}
	return nil
}

func (harness *Harness) Observe(
	ctx context.Context,
	action planning.Action,
) (adapters.Observation, error) {
	if err := harness.validate(); err != nil {
		return adapters.Observation{}, err
	}
	if err := validateHarnessAction(action); err != nil {
		return adapters.Observation{}, err
	}
	generation, err := harness.inspectGeneration(action)
	if err != nil {
		return adapters.Observation{
			State: adapters.StateConflict, Detail: err.Error(),
		}, nil
	}
	launcherPath, launcherContent, err := harness.launcher(action)
	if err != nil {
		return adapters.Observation{}, err
	}
	launcher := managedfile.Inspect(launcherPath, launcherContent)
	if generation == managedfile.StateMissing && launcher.State == managedfile.StateReady {
		return adapters.Observation{
			State:  adapters.StateConflict,
			Detail: "managed harness launcher references an absent payload",
		}, nil
	}
	if generation == managedfile.StateMissing || launcher.State == managedfile.StateMissing {
		return adapters.Observation{State: adapters.StateAbsent}, nil
	}
	if launcher.State == managedfile.StateConflict {
		return adapters.Observation{
			State:  adapters.StateConflict,
			Detail: "existing harness launcher is user-owned or unreadable",
		}, nil
	}
	preview, err := harness.Runtime.Preview(ctx, harness.stableRequest(action))
	if err != nil {
		return adapters.Observation{
			State:  adapters.StateConflict,
			Detail: "installed harness readiness preview failed",
		}, nil
	}
	if preview.Digest != action.Inputs["harness_child_digest"] {
		return adapters.Observation{
			State:  adapters.StateConflict,
			Detail: "installed harness child digest differs from the approved digest",
		}, nil
	}
	return adapters.Observation{
		State: adapters.StateReady, InstalledVersion: action.Version,
	}, nil
}

func (harness *Harness) Apply(
	ctx context.Context,
	action planning.Action,
) error {
	if err := harness.validate(); err != nil {
		return err
	}
	if err := validateHarnessAction(action); err != nil {
		return err
	}
	harness.mu.Lock()
	session := harness.session
	harness.mu.Unlock()
	if session == nil || session.actionID != action.ID {
		return errors.New("host harness apply requires a retained plan-wide preflight")
	}
	actionDigest, err := digestHarnessAction(action)
	if err != nil || actionDigest != session.actionDigest {
		return errors.New("host harness action changed after plan-wide preflight")
	}
	if err := session.verifySnapshotPreimages(); err != nil {
		return err
	}
	if err := harness.publishGeneration(action, session.sourceRoot); err != nil {
		return err
	}
	result, err := harness.Runtime.Apply(
		ctx, session.request, action.Inputs["harness_child_digest"],
	)
	if err != nil {
		return fmt.Errorf("apply approved child harness plan: %w", err)
	}
	if result.Status != "ready" ||
		result.Digest != action.Inputs["harness_child_digest"] {
		return errors.New("child harness apply did not return the approved ready state")
	}
	launcherPath, launcherContent, err := harness.launcher(action)
	if err != nil {
		return err
	}
	if err := managedfile.Publish(launcherPath, launcherContent); err != nil {
		var conflict *managedfile.ConflictError
		if errors.As(err, &conflict) {
			return errors.New("existing harness launcher will not be overwritten")
		}
		return fmt.Errorf("publish harness launcher: %w", err)
	}
	return nil
}

func (harness *Harness) Verify(
	ctx context.Context,
	action planning.Action,
) error {
	observation, err := harness.Observe(ctx, action)
	if err != nil {
		return err
	}
	if observation.State != adapters.StateReady {
		return fmt.Errorf("host harness is not ready: %s", observation.Detail)
	}
	return nil
}

func (harness *Harness) validate() error {
	if harness == nil || harness.Runtime == nil || harness.Composer.Acquirer == nil {
		return errors.New("host harness requires runtime and artifact acquirer")
	}
	if harness.Home == "" || !filepath.IsAbs(harness.Home) {
		return errors.New("host harness home must be absolute")
	}
	if harness.Platform != "darwin" && harness.Platform != "windows" {
		return errors.New("host harness supports only darwin and windows")
	}
	return nil
}

func harnessAction(plan planning.Plan) (planning.Action, error) {
	var result planning.Action
	count := 0
	for _, action := range plan.Actions {
		if action.ComponentID == "oh-my-harness" {
			result = action
			count++
		}
	}
	if count != 1 {
		return planning.Action{}, errors.New("plan must contain exactly one host harness action")
	}
	return result, nil
}

func recomposedActionInput(plan planning.Plan, componentID, key string) string {
	for _, action := range plan.Actions {
		if action.ComponentID == componentID {
			return action.Inputs[key]
		}
	}
	return ""
}

func digestHarnessAction(action planning.Action) (string, error) {
	encoded, err := json.Marshal(action)
	if err != nil {
		return "", fmt.Errorf("encode host harness action: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func validateHarnessAction(action planning.Action) error {
	if action.ComponentID != "oh-my-harness" || action.ID == "" ||
		action.Version == "" || action.Inputs == nil {
		return errors.New("invalid host harness action contract")
	}
	for _, key := range []string{
		"artifact_archive_sha256", "harness_child_digest",
		"harness_child_catalog_revision",
	} {
		value := action.Inputs[key]
		if len(value) != sha256.Size*2 {
			return fmt.Errorf("host harness action input %s is invalid", key)
		}
		if _, err := hex.DecodeString(value); err != nil || strings.ToLower(value) != value {
			return fmt.Errorf("host harness action input %s is invalid", key)
		}
	}
	configDigest := action.Inputs["harness_config_digest"]
	if !strings.HasPrefix(configDigest, "sha256:") ||
		len(configDigest) != len("sha256:")+sha256.Size*2 {
		return errors.New("host harness action input harness_config_digest is invalid")
	}
	configHex := strings.TrimPrefix(configDigest, "sha256:")
	if _, err := hex.DecodeString(configHex); err != nil ||
		strings.ToLower(configHex) != configHex {
		return errors.New("host harness action input harness_config_digest is invalid")
	}
	if action.Inputs["harness_release_version"] != action.Version {
		return errors.New("host harness release version is inconsistent")
	}
	if _, err := selectedHarnessAgents(action); err != nil {
		return err
	}
	return nil
}

func selectedHarnessAgents(action planning.Action) ([]string, error) {
	value := action.Inputs["harness_selected_agents"]
	if value == "none" {
		return nil, nil
	}
	wanted := map[string]bool{
		"claude-code": true, "codex": true, "opencode": true,
	}
	parts := strings.Split(value, ",")
	if len(parts) == 0 || len(parts) > len(wanted) {
		return nil, errors.New("host harness selected agents are invalid")
	}
	for index, id := range parts {
		if !wanted[id] || (index > 0 && parts[index-1] >= id) {
			return nil, errors.New("host harness selected agents are invalid")
		}
	}
	return parts, nil
}

func (harness *Harness) generationPath(action planning.Action) string {
	return filepath.Join(
		harness.Home, ".local", "share", "my-desk-setup", "oh-my-harness",
		action.Inputs["artifact_archive_sha256"],
	)
}

func (harness *Harness) launcher(action planning.Action) (string, string, error) {
	generation := harness.generationPath(action)
	entrypoint := filepath.Join(generation, "package", "dist", "cli", "main.js")
	nodeName := "node"
	launcherName := "omh"
	if harness.Platform == "windows" {
		nodeName += ".exe"
		launcherName += ".cmd"
		return filepath.Join(harness.Home, ".local", "bin", launcherName),
			strings.Join([]string{
				"@echo off",
				"rem Managed by my-desk-setup. Authentication remains user-owned.",
				"\"" + filepath.Join(harness.Home, ".local", "bin", nodeName) +
					"\" \"" + entrypoint + "\" %*",
				"",
			}, "\r\n"), nil
	}
	return filepath.Join(harness.Home, ".local", "bin", launcherName),
		strings.Join([]string{
			"#!/bin/sh",
			"# Managed by my-desk-setup. Authentication remains user-owned.",
			"exec " + adapters.ShellSingleQuote(
				filepath.Join(harness.Home, ".local", "bin", nodeName),
			) + " " + adapters.ShellSingleQuote(entrypoint) + ` "$@"`,
			"",
		}, "\n"), nil
}

func (harness *Harness) stableRequest(action planning.Action) harnessruntime.Request {
	agents, _ := selectedHarnessAgents(action)
	agentExecutables := make(map[string]string, len(agents))
	for _, id := range agents {
		name := id
		if id == "claude-code" {
			name = "claude"
		}
		if harness.Platform == "windows" {
			name += ".exe"
		}
		agentExecutables[id] = filepath.Join(harness.Home, ".local", "bin", name)
	}
	nodeName := "node"
	if harness.Platform == "windows" {
		nodeName += ".exe"
	}
	request := harnessruntime.Request{
		NodeExecutable: filepath.Join(harness.Home, ".local", "bin", nodeName),
		Entrypoint: filepath.Join(
			harness.generationPath(action), "package", "dist", "cli", "main.js",
		),
		StateRoot: harness.Composer.StateRoot, Home: harness.Home,
		ConfigRoot: harness.Composer.ConfigRoot, TempRoot: harness.Composer.TempRoot,
		Platform: harness.Platform, Locale: harness.Composer.Locale,
		SystemRoot: harness.Composer.SystemRoot, ComSpec: harness.Composer.ComSpec,
		AppData: harness.Composer.AppData, LocalAppData: harness.Composer.LocalAppData,
		PathExt: harness.Composer.PathExt, AgentExecutables: agentExecutables,
		ManagedAgentIdentities: managedAgentIdentities(action),
		Timeout:                harness.Composer.Timeout,
	}
	return request
}

func managedAgentIdentities(action planning.Action) []string {
	value := action.Inputs["harness_agent_identities"]
	if value == "" || value == "none" {
		return nil
	}
	return strings.Split(value, ",")
}

func (harness *Harness) validateDestination(action planning.Action) error {
	generation, err := harness.inspectGeneration(action)
	if err != nil {
		return err
	}
	launcherPath, launcherContent, err := harness.launcher(action)
	if err != nil {
		return err
	}
	launcher := managedfile.Inspect(launcherPath, launcherContent)
	if launcher.State == managedfile.StateConflict {
		return errors.New("existing harness launcher is user-owned or unreadable")
	}
	if generation == managedfile.StateMissing && launcher.State == managedfile.StateReady {
		return errors.New("managed harness launcher references an absent payload")
	}
	return nil
}

func (harness *Harness) inspectGeneration(
	action planning.Action,
) (managedfile.State, error) {
	root := harness.generationPath(action)
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return managedfile.StateMissing, nil
	}
	if err != nil {
		return managedfile.StateConflict, fmt.Errorf("inspect harness payload: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return managedfile.StateConflict, errors.New("harness payload path is not a managed directory")
	}
	marker, err := readHarnessMarker(filepath.Join(root, harnessMarkerName))
	if err != nil {
		return managedfile.StateConflict, fmt.Errorf("read harness payload marker: %w", err)
	}
	if marker.Schema != harnessMarkerSchema || marker.Version != action.Version ||
		marker.ArchiveSHA256 != action.Inputs["artifact_archive_sha256"] {
		return managedfile.StateConflict, errors.New("harness payload marker identity differs")
	}
	digest, _, _, err := harnessTreeDigest(root, harnessMarkerName)
	if err != nil {
		return managedfile.StateConflict, fmt.Errorf("verify harness payload: %w", err)
	}
	if digest != marker.TreeSHA256 {
		return managedfile.StateConflict, errors.New("harness payload content digest differs")
	}
	return managedfile.StateReady, nil
}

func (harness *Harness) publishGeneration(
	action planning.Action,
	sourceRoot string,
) (returnErr error) {
	state, err := harness.inspectGeneration(action)
	if err != nil {
		return err
	}
	if state == managedfile.StateReady {
		return nil
	}
	parent := filepath.Dir(harness.generationPath(action))
	if err := ensureHarnessDirectory(harness.Home, parent); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".mds-harness-staging-*")
	if err != nil {
		return fmt.Errorf("create harness payload staging directory: %w", err)
	}
	defer func() {
		if staging != "" {
			returnErr = errors.Join(returnErr, os.RemoveAll(staging))
		}
	}()
	if err := copyHarnessTree(sourceRoot, staging); err != nil {
		return err
	}
	digest, _, _, err := harnessTreeDigest(staging, harnessMarkerName)
	if err != nil {
		return err
	}
	marker := harnessGenerationMarker{
		Schema: harnessMarkerSchema, Version: action.Version,
		ArchiveSHA256: action.Inputs["artifact_archive_sha256"], TreeSHA256: digest,
	}
	content, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("encode harness payload marker: %w", err)
	}
	content = append(content, '\n')
	if err := os.WriteFile(filepath.Join(staging, harnessMarkerName), content, 0o600); err != nil {
		return fmt.Errorf("write harness payload marker: %w", err)
	}
	if err := durable.PublishDirectoryNoReplace(staging, harness.generationPath(action)); err != nil {
		if ready, inspectErr := harness.inspectGeneration(action); inspectErr == nil && ready == managedfile.StateReady {
			return nil
		}
		return fmt.Errorf("publish harness payload: %w", err)
	}
	staging = ""
	return nil
}

func readHarnessMarker(path string) (harnessGenerationMarker, error) {
	file, err := os.Open(path)
	if err != nil {
		return harnessGenerationMarker{}, err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return harnessGenerationMarker{}, err
	}
	if len(content) > 4096 {
		return harnessGenerationMarker{}, errors.New("marker exceeds size limit")
	}
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	var marker harnessGenerationMarker
	if err := decoder.Decode(&marker); err != nil {
		return harnessGenerationMarker{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return harnessGenerationMarker{}, errors.New("marker has trailing content")
	}
	return marker, nil
}

func copyHarnessTree(sourceRoot, destinationRoot string) error {
	_, entries, bytes, err := harnessTreeDigest(sourceRoot, harnessMarkerName)
	if err != nil {
		return fmt.Errorf("validate harness source tree: %w", err)
	}
	if entries > maxHarnessEntries || bytes > maxHarnessBytes {
		return errors.New("harness source tree exceeds publication bounds")
	}
	return filepath.WalkDir(sourceRoot, func(
		path string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if filepath.ToSlash(relative) == harnessMarkerName {
			return errors.New("harness source tree contains a reserved marker")
		}
		destination := filepath.Join(destinationRoot, relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("harness source tree contains symlink %s", relative)
		}
		if entry.IsDir() {
			return os.Mkdir(destination, 0o700)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("harness source tree contains non-regular path %s", relative)
		}
		return copyHarnessFile(path, destination, entry)
	})
}

func copyHarnessFile(source, destination string, entry fs.DirEntry) (returnErr error) {
	info, err := entry.Info()
	if err != nil {
		return err
	}
	permission := os.FileMode(0o600)
	if info.Mode().Perm()&0o111 != 0 {
		permission = 0o700
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, input.Close()) }()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, permission)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, output.Close()) }()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	return nil
}

func harnessTreeDigest(
	root,
	excludedRelative string,
) (string, int, int64, error) {
	hasher := sha256.New()
	entries := 0
	var totalBytes int64
	paths := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == "." || filepath.ToSlash(relative) == excludedRelative {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("tree contains symlink %s", relative)
		}
		if !entry.IsDir() && !entry.Type().IsRegular() {
			return fmt.Errorf("tree contains non-regular path %s", relative)
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return "", 0, 0, err
	}
	sort.Slice(paths, func(left, right int) bool {
		leftRelative, _ := filepath.Rel(root, paths[left])
		rightRelative, _ := filepath.Rel(root, paths[right])
		return filepath.ToSlash(leftRelative) < filepath.ToSlash(rightRelative)
	})
	for _, path := range paths {
		entry, err := os.Lstat(path)
		if err != nil {
			return "", 0, 0, err
		}
		relative, _ := filepath.Rel(root, path)
		kind := byte('d')
		if entry.Mode().IsRegular() {
			kind = 'f'
		}
		_, _ = hasher.Write([]byte{kind})
		canonicalRelative := []byte(filepath.ToSlash(relative))
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(canonicalRelative)))
		_, _ = hasher.Write(length[:])
		_, _ = hasher.Write(canonicalRelative)
		entries++
		if entries > maxHarnessEntries {
			return "", entries, totalBytes, errors.New("tree entry limit exceeded")
		}
		if kind == 'd' {
			continue
		}
		binary.BigEndian.PutUint64(length[:], uint64(entry.Size()))
		_, _ = hasher.Write(length[:])
		file, err := os.Open(path)
		if err != nil {
			return "", 0, 0, err
		}
		copied, copyErr := io.Copy(hasher, io.LimitReader(file, maxHarnessBytes-totalBytes+1))
		closeErr := file.Close()
		totalBytes += copied
		if copyErr != nil || closeErr != nil {
			return "", 0, 0, errors.Join(copyErr, closeErr)
		}
		if totalBytes > maxHarnessBytes {
			return "", entries, totalBytes, errors.New("tree byte limit exceeded")
		}
	}
	return hex.EncodeToString(hasher.Sum(nil)), entries, totalBytes, nil
}

func ensureHarnessDirectory(home, destination string) error {
	home = filepath.Clean(home)
	destination = filepath.Clean(destination)
	if !pathWithin(home, destination) {
		return errors.New("harness destination escapes home")
	}
	relative, err := filepath.Rel(home, destination)
	if err != nil {
		return err
	}
	current := home
	for _, element := range strings.Split(relative, string(filepath.Separator)) {
		if element == "" || element == "." || element == ".." {
			return errors.New("invalid harness destination component")
		}
		current = filepath.Join(current, element)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil {
				return fmt.Errorf("create harness directory: %w", err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect harness directory: %w", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("harness directory is not a real directory: %s", current)
		}
	}
	return nil
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
