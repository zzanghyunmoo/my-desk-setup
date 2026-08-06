package planning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/harness"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
)

const (
	harnessComponentID = "oh-my-harness"
	nodeComponentID    = "omh-node-runtime"
	harnessEntrypoint  = "package/dist/cli/main.js"
)

var (
	exactCompositionAgents    = []string{"claude-code", "codex", "opencode"}
	exactCompositionWorkflows = []string{
		"brainstorm",
		"code-review",
		"deep-research",
		"doc-review",
		"goal",
		"ideation",
		"plan",
		"ralph-loop",
		"security-guidance",
		"skill-creator",
	}
)

type VerifiedSnapshot interface {
	Root() string
	Executable() string
	Path(string) string
	Close() error
}

type SnapshotAcquirer interface {
	Acquire(context.Context, artifact.SnapshotRequest) (VerifiedSnapshot, error)
}

// ArtifactSnapshotAcquirer adapts the production verified snapshotter without
// forcing pure composition tests to perform filesystem or network I/O.
type ArtifactSnapshotAcquirer struct {
	Snapshotter artifact.Snapshotter
}

func (acquirer ArtifactSnapshotAcquirer) Acquire(
	ctx context.Context,
	request artifact.SnapshotRequest,
) (VerifiedSnapshot, error) {
	return acquirer.Snapshotter.Acquire(ctx, request)
}

type ChildPreviewer interface {
	Preview(context.Context, harness.Request) (harness.Result, error)
}

type Composer struct {
	Acquirer  SnapshotAcquirer
	Previewer ChildPreviewer

	Home         string
	ConfigRoot   string
	TempRoot     string
	StateRoot    string
	Locale       string
	SystemRoot   string
	ComSpec      string
	AppData      string
	LocalAppData string
	PathExt      string
	Timeout      time.Duration
}

type ArtifactIdentity struct {
	ComponentID      string `json:"component_id"`
	Version          string `json:"version"`
	ArchiveSHA256    string `json:"archive_sha256"`
	ExecutableSHA256 string `json:"executable_sha256,omitempty"`
	SourceDigest     string `json:"source_digest"`
}

type Composition struct {
	Child     harness.Result
	Artifacts []ArtifactIdentity
}

type CompositionError struct {
	Code string
}

func (err *CompositionError) Error() string {
	if err == nil || err.Code == "" {
		return "oh-my-harness composition failed"
	}
	return "oh-my-harness composition failed: " + err.Code
}

type snapshotSpecification struct {
	identity ArtifactIdentity
	request  artifact.SnapshotRequest
}

type acquiredSnapshot struct {
	componentID string
	snapshot    VerifiedSnapshot
}

func (composer Composer) Compose(
	ctx context.Context,
	environment catalog.Environment,
	base Plan,
) (result Plan, resultErr error) {
	if !shouldCompose(base) {
		return base, nil
	}
	if composer.Acquirer == nil || composer.Previewer == nil {
		return blockedComposition(base, "composer-contract"), nil
	}
	baseDigest, err := Digest(base)
	if err != nil || base.Digest != baseDigest {
		return blockedComposition(base, "base-stale"), nil
	}
	selectedAgents := selectedCompositionAgents(base)
	specifications, err := compositionSpecifications(
		environment,
		base,
		selectedAgents,
	)
	if err != nil {
		return blockedComposition(base, "artifact-contract"), nil
	}

	acquired := make([]acquiredSnapshot, 0, len(specifications))
	defer func() {
		cleanupFailed := false
		for index := len(acquired) - 1; index >= 0; index-- {
			if err := acquired[index].snapshot.Close(); err != nil {
				cleanupFailed = true
			}
		}
		if cleanupFailed {
			result = blockedComposition(base, "snapshot-cleanup")
			resultErr = nil
		}
	}()
	byID := make(map[string]VerifiedSnapshot, len(specifications))
	for _, specification := range specifications {
		snapshot, err := composer.Acquirer.Acquire(ctx, specification.request)
		if err != nil || snapshot == nil {
			return blockedComposition(base, "artifact-acquisition"), nil
		}
		acquired = append(acquired, acquiredSnapshot{
			componentID: specification.identity.ComponentID,
			snapshot:    snapshot,
		})
		byID[specification.identity.ComponentID] = snapshot
	}

	nodeSnapshot := byID[nodeComponentID]
	harnessSnapshot := byID[harnessComponentID]
	if nodeSnapshot == nil || harnessSnapshot == nil {
		return blockedComposition(base, "artifact-contract"), nil
	}
	agentExecutables := make(map[string]string, len(selectedAgents))
	for _, id := range selectedAgents {
		snapshot := byID[id]
		if snapshot == nil {
			return blockedComposition(base, "artifact-contract"), nil
		}
		agentExecutables[id] = snapshot.Executable()
	}
	child, err := composer.Previewer.Preview(ctx, harness.Request{
		NodeExecutable:         nodeSnapshot.Executable(),
		Entrypoint:             harnessSnapshot.Path(harnessEntrypoint),
		StateRoot:              composer.StateRoot,
		Home:                   composer.Home,
		ConfigRoot:             composer.ConfigRoot,
		TempRoot:               composer.TempRoot,
		Platform:               base.Target.OS,
		Locale:                 composer.Locale,
		SystemRoot:             composer.SystemRoot,
		ComSpec:                composer.ComSpec,
		AppData:                composer.AppData,
		LocalAppData:           composer.LocalAppData,
		PathExt:                composer.PathExt,
		AgentExecutables:       agentExecutables,
		ManagedAgentIdentities: runtimeIdentities(specifications, selectedAgents),
		Timeout:                composer.Timeout,
	})
	if err != nil {
		code := "child-preview"
		var childError *harness.Error
		if errors.As(err, &childError) && safeCode(childError.Code) {
			code = "child-" + childError.Code
		}
		return blockedComposition(base, code), nil
	}
	if child.Readiness != "preview" || child.Digest == "" {
		code := "child-blocked"
		if blockers, ok := safeChildBlockerIDs(child.Blockers); ok && len(blockers) > 0 {
			code += " (" + strings.Join(blockers, ",") + ")"
		}
		return blockedComposition(base, code), nil
	}
	composed, err := Compose(base, Composition{
		Child: child, Artifacts: identities(specifications),
	})
	if err != nil {
		return blockedComposition(base, "child-contract"), nil
	}
	return composed, nil
}

// Compose is the pure, deterministic projection from a verified child result
// into an outer plan. It never performs acquisition or observes local state.
func Compose(base Plan, composition Composition) (Plan, error) {
	if !shouldCompose(base) {
		return base, nil
	}
	selectedAgents := selectedCompositionAgents(base)
	child := composition.Child
	if child.SchemaVersion != "2.0.0" || child.Readiness != "preview" ||
		!validSHA256(child.Digest) || !validSHA256(child.CatalogRevision) ||
		!validPrefixedSHA256(child.ConfigDigest) ||
		!equalStringSlices(child.SelectedAgents, selectedAgents) ||
		!equalStringSlices(child.Workflows, exactCompositionWorkflows) ||
		len(child.Blockers) != 0 ||
		!validChildOwnership(child.Ownership, selectedAgents) ||
		!validChildAddons(child.Addons, selectedAgents) {
		return Plan{}, &CompositionError{Code: "child-contract"}
	}

	identityByID := make(map[string]ArtifactIdentity, len(composition.Artifacts))
	for _, identity := range composition.Artifacts {
		if _, duplicate := identityByID[identity.ComponentID]; duplicate ||
			identity.Version == "" || !validSHA256(identity.ArchiveSHA256) ||
			!validPrefixedSHA256(identity.SourceDigest) ||
			(identity.ExecutableSHA256 != "" && !validSHA256(identity.ExecutableSHA256)) {
			return Plan{}, &CompositionError{Code: "artifact-contract"}
		}
		identityByID[identity.ComponentID] = identity
	}
	wanted := append([]string{nodeComponentID, harnessComponentID}, selectedAgents...)
	sort.Strings(wanted)
	if len(identityByID) != len(wanted) {
		return Plan{}, &CompositionError{Code: "artifact-contract"}
	}
	for _, id := range wanted {
		if _, exists := identityByID[id]; !exists {
			return Plan{}, &CompositionError{Code: "artifact-contract"}
		}
	}

	result := clonePlan(base)
	for index := range result.Actions {
		action := &result.Actions[index]
		identity, exists := identityByID[action.ComponentID]
		if !exists {
			continue
		}
		action.Inputs = cloneInputs(action.Inputs)
		action.Inputs["artifact_archive_sha256"] = identity.ArchiveSHA256
		action.Inputs["artifact_source_digest"] = identity.SourceDigest
		if identity.ExecutableSHA256 != "" {
			action.Inputs["artifact_executable_sha256"] = identity.ExecutableSHA256
		}
		if action.ComponentID != harnessComponentID {
			continue
		}
		for _, agentID := range selectedAgents {
			agentAction, exists := planAction(result, agentID)
			if !exists || contains(action.Dependencies, agentAction.ID) {
				continue
			}
			action.Dependencies = append(action.Dependencies, agentAction.ID)
		}
		sort.Strings(action.Dependencies)
		agentIdentities := runtimeIdentitiesFromMap(identityByID, selectedAgents)
		action.Inputs["harness_schema_version"] = child.SchemaVersion
		action.Inputs["harness_child_digest"] = child.Digest
		action.Inputs["harness_child_catalog_revision"] = child.CatalogRevision
		action.Inputs["harness_config_digest"] = child.ConfigDigest
		action.Inputs["harness_selected_agents"] = joinedOrNone(selectedAgents)
		action.Inputs["harness_workflows"] = strings.Join(child.Workflows, ",")
		action.Inputs["harness_agent_identities"] = joinedOrNone(agentIdentities)
		action.Inputs["harness_addon_summary_digest"] = summaryDigest(
			"my-desk-setup/omh-addon-summary/v1", child.Addons,
		)
		action.Inputs["harness_ownership_summary_digest"] = summaryDigest(
			"my-desk-setup/omh-ownership-summary/v1", child.Ownership,
		)
		action.Inputs["harness_release_version"] = identity.Version
		action.Inputs["harness_node_version"] = identityByID[nodeComponentID].Version
		action.Inputs["harness_node_archive_sha256"] = identityByID[nodeComponentID].ArchiveSHA256
	}
	var err error
	result.Digest, err = Digest(result)
	if err != nil {
		return Plan{}, &CompositionError{Code: "outer-digest"}
	}
	return result, nil
}

func shouldCompose(plan Plan) bool {
	if plan.Target.ID.Kind != target.KindMacOSHost &&
		plan.Target.ID.Kind != target.KindWindowsHost {
		return false
	}
	for _, action := range plan.Actions {
		if action.ComponentID == harnessComponentID {
			return true
		}
	}
	return false
}

func selectedCompositionAgents(plan Plan) []string {
	selected := make([]string, 0, len(exactCompositionAgents))
	for _, id := range exactCompositionAgents {
		for _, action := range plan.Actions {
			if action.ComponentID == id {
				selected = append(selected, id)
				break
			}
		}
	}
	return selected
}

func compositionSpecifications(
	environment catalog.Environment,
	base Plan,
	selectedAgents []string,
) ([]snapshotSpecification, error) {
	platform, err := compositionPlatform(base)
	if err != nil {
		return nil, err
	}
	ids := append([]string{nodeComponentID, harnessComponentID}, selectedAgents...)
	result := make([]snapshotSpecification, 0, len(ids))
	for _, id := range ids {
		component, lock, artifactValue, err := lockedArtifact(
			environment, id, platform,
		)
		if err != nil {
			return nil, err
		}
		action, exists := planAction(base, id)
		if !exists || action.Status != ActionPlanned ||
			action.Version != lock.Version || !validSHA256(artifactValue.SHA256) ||
			!safeArtifactURL(artifactValue.URL) || artifactValue.Executable == "" ||
			(artifactValue.Format != "tar.gz" && artifactValue.Format != "zip") {
			return nil, &CompositionError{Code: "artifact-contract"}
		}
		if contains(exactCompositionAgents, id) &&
			!validSHA256(artifactValue.ExecutableSHA256) {
			return nil, &CompositionError{Code: "artifact-contract"}
		}
		alias := executableAlias(id, base.Target.OS)
		result = append(result, snapshotSpecification{
			identity: ArtifactIdentity{
				ComponentID:      id,
				Version:          lock.Version,
				ArchiveSHA256:    artifactValue.SHA256,
				ExecutableSHA256: artifactValue.ExecutableSHA256,
				SourceDigest: summaryDigest(
					"my-desk-setup/artifact-source/v1",
					[]string{component.VersionPolicy.LockKey, lock.Source, lock.Provenance},
				),
			},
			request: artifact.SnapshotRequest{
				URL:              artifactValue.URL,
				SHA256:           artifactValue.SHA256,
				Format:           artifactValue.Format,
				Executable:       artifactValue.Executable,
				ExecutableSHA256: artifactValue.ExecutableSHA256,
				Alias:            alias,
				ExtractAll:       id == harnessComponentID,
			},
		})
	}
	return result, nil
}

func safeArtifactURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" &&
		parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func safeChildBlockerIDs(values []string) ([]string, bool) {
	if len(values) > 32 {
		return nil, false
	}
	result := append([]string{}, values...)
	sort.Strings(result)
	for index, value := range result {
		if value == "" || len(value) > 128 ||
			(index > 0 && result[index-1] == value) {
			return nil, false
		}
		for _, character := range value {
			if (character < 'a' || character > 'z') &&
				(character < '0' || character > '9') &&
				character != '.' && character != ':' && character != '_' &&
				character != '-' {
				return nil, false
			}
		}
	}
	return result, true
}

func compositionPlatform(base Plan) (string, error) {
	osName := base.Target.OS
	if base.Target.ID.Kind == target.KindMacOSHost && osName != "darwin" {
		return "", &CompositionError{Code: "target-contract"}
	}
	if base.Target.ID.Kind == target.KindWindowsHost && osName != "windows" {
		return "", &CompositionError{Code: "target-contract"}
	}
	if base.Target.Architecture != "arm64" && base.Target.Architecture != "amd64" {
		return "", &CompositionError{Code: "target-contract"}
	}
	return osName + "-" + base.Target.Architecture, nil
}

func lockedArtifact(
	environment catalog.Environment,
	id,
	platform string,
) (catalog.Component, catalog.LockEntry, catalog.Artifact, error) {
	for _, component := range environment.Catalog.Components {
		if component.ID != id || component.VersionPolicy.Mode != "pinned" ||
			component.VersionPolicy.LockKey == "" {
			continue
		}
		lock, exists := environment.Lock.Versions[component.VersionPolicy.LockKey]
		if !exists || lock.Version == "" {
			break
		}
		artifactValue, exists := lock.Artifacts[platform]
		if !exists {
			break
		}
		return component, lock, artifactValue, nil
	}
	return catalog.Component{}, catalog.LockEntry{}, catalog.Artifact{},
		&CompositionError{Code: "artifact-contract"}
}

func executableAlias(id, platform string) string {
	alias := id
	if id == nodeComponentID {
		return ""
	}
	if id == harnessComponentID {
		return ""
	}
	if id == "claude-code" {
		alias = "claude"
	}
	if platform == "windows" {
		alias += ".exe"
	}
	return alias
}

func blockedComposition(base Plan, code string) Plan {
	result := clonePlan(base)
	reason := "oh-my-harness composition is action-required: " + code
	harnessActionID := ""
	for index := range result.Actions {
		if result.Actions[index].ComponentID != harnessComponentID {
			continue
		}
		result.Actions[index].Status = ActionActionRequired
		result.Actions[index].Reason = reason
		harnessActionID = result.Actions[index].ID
		break
	}
	if harnessActionID != "" {
		blockers := result.Blockers[:0]
		for _, blocker := range result.Blockers {
			if blocker.ActionID != harnessActionID {
				blockers = append(blockers, blocker)
			}
		}
		result.Blockers = append(blockers, Blocker{
			ActionID: harnessActionID, Status: ActionActionRequired, Reason: reason,
		})
		sort.Slice(result.Blockers, func(left, right int) bool {
			return result.Blockers[left].ActionID < result.Blockers[right].ActionID
		})
	}
	result.Digest, _ = Digest(result)
	return result
}

func clonePlan(base Plan) Plan {
	result := base
	result.Selection = append([]string(nil), base.Selection...)
	result.Actions = make([]Action, len(base.Actions))
	for index, action := range base.Actions {
		result.Actions[index] = action
		result.Actions[index].Dependencies = append([]string(nil), action.Dependencies...)
		result.Actions[index].Verification = make([][]string, len(action.Verification))
		for commandIndex, command := range action.Verification {
			result.Actions[index].Verification[commandIndex] = append([]string(nil), command...)
		}
		result.Actions[index].Inputs = cloneInputs(action.Inputs)
	}
	result.Blockers = append([]Blocker(nil), base.Blockers...)
	return result
}

func cloneInputs(source map[string]string) map[string]string {
	result := make(map[string]string, len(source)+16)
	for key, value := range source {
		result[key] = value
	}
	return result
}

func identities(specifications []snapshotSpecification) []ArtifactIdentity {
	result := make([]ArtifactIdentity, 0, len(specifications))
	for _, specification := range specifications {
		result = append(result, specification.identity)
	}
	return result
}

func runtimeIdentities(specifications []snapshotSpecification, selectedAgents []string) []string {
	byID := make(map[string]ArtifactIdentity, len(specifications))
	for _, specification := range specifications {
		byID[specification.identity.ComponentID] = specification.identity
	}
	return runtimeIdentitiesFromMap(byID, selectedAgents)
}

func runtimeIdentitiesFromMap(byID map[string]ArtifactIdentity, selectedAgents []string) []string {
	result := make([]string, 0, len(selectedAgents))
	for _, id := range selectedAgents {
		identity := byID[id]
		result = append(result, id+"@"+identity.Version+":"+
			identity.ArchiveSHA256+":"+identity.ExecutableSHA256)
	}
	return result
}

func summaryDigest(domain string, value any) string {
	encoded, _ := json.Marshal(struct {
		Domain string `json:"domain"`
		Value  any    `json:"value"`
	}{Domain: domain, Value: value})
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func joinedOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ",")
}

func validSHA256(value string) bool {
	return artifact.ValidateSHA256(value) == nil
}

func validPrefixedSHA256(value string) bool {
	return strings.HasPrefix(value, "sha256:") && validSHA256(strings.TrimPrefix(value, "sha256:"))
}

func safeCode(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}

func planAction(plan Plan, id string) (Action, bool) {
	for _, action := range plan.Actions {
		if action.ComponentID == id {
			return action, true
		}
	}
	return Action{}, false
}

func validChildOwnership(values []harness.Ownership, selected []string) bool {
	if len(values) != len(selected) {
		return false
	}
	for index, value := range values {
		if value.ID != selected[index] || value.Ownership != "external" ||
			value.State != "ready" {
			return false
		}
	}
	return true
}

func validChildAddons(values []harness.Addon, selected []string) bool {
	wanted := make([]string, 0, 2)
	for _, id := range selected {
		if id == "codex" || id == "opencode" {
			wanted = append(wanted, id)
		}
	}
	if len(values) != len(wanted) {
		return false
	}
	for index, value := range values {
		if value.AgentID != wanted[index] || value.ID != "omo" ||
			value.Version != "4.19.2" || !validSHA256(value.Fingerprint) ||
			(value.State != "installable" && value.State != "ready") {
			return false
		}
	}
	return true
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
