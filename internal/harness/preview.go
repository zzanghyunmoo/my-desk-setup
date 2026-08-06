package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

const (
	childSchemaVersion = "2.0.0"
	childProfileID     = "mds-host"
	childOutputLimit   = 4 << 20
	defaultTimeout     = 45 * time.Second
	maximumTimeout     = 2 * time.Minute
)

var (
	exactAgentIDs  = []string{"claude-code", "codex", "opencode"}
	exactWorkflows = []string{
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
	summaryIDPattern       = regexp.MustCompile(`^[a-z0-9]+(?:[.:_-][a-z0-9]+)*$`)
	sha256Pattern          = regexp.MustCompile(`^[a-f0-9]{64}$`)
	runtimeIdentityPattern = regexp.MustCompile(
		`^(claude-code|codex|opencode)@([^:]{1,128}):([a-f0-9]{64}):([a-f0-9]{64})$`,
	)
)

type Request struct {
	NodeExecutable         string
	Entrypoint             string
	StateRoot              string
	Home                   string
	ConfigRoot             string
	TempRoot               string
	Platform               string
	Locale                 string
	SystemRoot             string
	ComSpec                string
	AppData                string
	LocalAppData           string
	PathExt                string
	AgentExecutables       map[string]string
	ManagedAgentIdentities []string
	Timeout                time.Duration
}

type Addon struct {
	AgentID     string `json:"agent_id"`
	ID          string `json:"id"`
	Version     string `json:"version"`
	State       string `json:"state"`
	Fingerprint string `json:"fingerprint"`
}

type Ownership struct {
	ID        string `json:"id"`
	Ownership string `json:"ownership"`
	State     string `json:"state"`
}

// Result is the secret-free, path-free portion of an OMH child preview that
// may be bound into an outer approval digest.
type Result struct {
	SchemaVersion   string      `json:"schema_version"`
	Digest          string      `json:"digest,omitempty"`
	CatalogRevision string      `json:"catalog_revision"`
	Readiness       string      `json:"readiness"`
	SelectedAgents  []string    `json:"selected_agents"`
	Workflows       []string    `json:"workflows"`
	Addons          []Addon     `json:"addons"`
	Ownership       []Ownership `json:"ownership"`
	ConfigDigest    string      `json:"config_digest"`
	Blockers        []string    `json:"blockers"`
	OptionalGapIDs  []string    `json:"optional_gap_ids"`
}

type Error struct {
	Code string
}

func (err *Error) Error() string {
	if err == nil || err.Code == "" {
		return "omh child preview failed"
	}
	return "omh child preview failed: " + err.Code
}

type Runner struct {
	Port transport.Port
}

func (runner Runner) Preview(ctx context.Context, request Request) (Result, error) {
	if runner.Port == nil {
		return Result{}, &Error{Code: "transport"}
	}
	agents, trustedDirectories, err := validateRequest(request)
	if err != nil {
		return Result{}, err
	}
	timeout := request.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	if timeout < 0 || timeout > maximumTimeout {
		return Result{}, &Error{Code: "timeout-contract"}
	}
	environment := isolatedEnvironment(request, trustedDirectories)
	agentArgument := "none"
	if len(agents) > 0 {
		agentArgument = strings.Join(agents, ",")
	}
	command := transport.Command{
		Executable: request.NodeExecutable,
		Arguments: []string{
			request.Entrypoint,
			"setup",
			"--profile",
			childProfileID,
			"--agents",
			agentArgument,
			"--root",
			request.StateRoot,
			"--json",
		},
		Environment:      environment,
		EnvironmentMode:  transport.EnvironmentReplace,
		WorkingDirectory: request.TempRoot,
		Timeout:          timeout,
		OutputLimit:      childOutputLimit,
	}
	processResult, runErr := runner.Port.Run(ctx, command)
	if runErr != nil {
		if errors.Is(runErr, context.DeadlineExceeded) ||
			strings.Contains(runErr.Error(), "timed out") {
			return Result{}, &Error{Code: "timeout"}
		}
		var commandErr *transport.CommandError
		if !errors.As(runErr, &commandErr) ||
			(processResult.ExitCode != 2 && processResult.ExitCode != 3) {
			return Result{}, &Error{Code: "process"}
		}
	}
	if strings.Contains(processResult.Stdout, "[output truncated]") ||
		strings.Contains(processResult.Stderr, "[output truncated]") ||
		len(processResult.Stdout) > childOutputLimit {
		return Result{}, &Error{Code: "output-limit"}
	}
	envelope, err := decodeEnvelope(processResult.Stdout)
	if err != nil {
		return Result{}, err
	}
	return validateEnvelope(envelope, processResult.ExitCode, agents)
}

func validateRequest(request Request) ([]string, []string, error) {
	for _, value := range []struct {
		name string
		path string
	}{
		{name: "node-executable", path: request.NodeExecutable},
		{name: "entrypoint", path: request.Entrypoint},
	} {
		if err := requireRegularAbsolute(value.path); err != nil {
			return nil, nil, &Error{Code: value.name}
		}
	}
	for _, value := range []struct {
		name string
		path string
	}{
		{name: "state-root", path: request.StateRoot},
		{name: "home", path: request.Home},
		{name: "config-root", path: request.ConfigRoot},
		{name: "temp-root", path: request.TempRoot},
	} {
		if value.path == "" || !filepath.IsAbs(value.path) ||
			strings.ContainsRune(value.path, '\x00') {
			return nil, nil, &Error{Code: value.name}
		}
	}
	if request.Platform != "darwin" && request.Platform != "windows" {
		return nil, nil, &Error{Code: "platform"}
	}
	for _, value := range []string{
		request.Locale,
		request.SystemRoot,
		request.ComSpec,
		request.AppData,
		request.LocalAppData,
		request.PathExt,
	} {
		if strings.ContainsRune(value, '\x00') {
			return nil, nil, &Error{Code: "environment"}
		}
	}
	agents := sortedAgentIDs(request.AgentExecutables)
	for _, id := range agents {
		if !contains(exactAgentIDs, id) {
			return nil, nil, &Error{Code: "selected-agent"}
		}
		executable := request.AgentExecutables[id]
		if err := requireRegularAbsolute(executable); err != nil {
			return nil, nil, &Error{Code: "agent-executable"}
		}
		base := strings.ToLower(filepath.Base(executable))
		wanted := id
		if id == "claude-code" {
			wanted = "claude"
		}
		if base != wanted && base != wanted+".exe" {
			return nil, nil, &Error{Code: "agent-executable"}
		}
	}
	if err := validateRuntimeIdentities(
		request.ManagedAgentIdentities, agents, request.AgentExecutables,
	); err != nil {
		return nil, nil, &Error{Code: "runtime-identity"}
	}
	directories := []string{filepath.Dir(request.NodeExecutable)}
	for _, id := range agents {
		directories = append(directories, filepath.Dir(request.AgentExecutables[id]))
	}
	sort.Strings(directories)
	directories = compact(directories)
	return agents, directories, nil
}

func validateRuntimeIdentities(
	identities []string,
	agents []string,
	executables map[string]string,
) error {
	if len(identities) == 0 {
		return nil
	}
	if len(identities) != len(agents) {
		return errors.New("runtime identities must match selected agents")
	}
	for index, identity := range identities {
		match := runtimeIdentityPattern.FindStringSubmatch(identity)
		if len(match) != 5 || match[1] != agents[index] {
			return errors.New("invalid runtime identity")
		}
		actual, err := executableSHA256(executables[agents[index]])
		if err != nil || actual != match[4] {
			return errors.New("runtime executable digest differs")
		}
	}
	return nil
}

func executableSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func requireRegularAbsolute(value string) error {
	if value == "" || !filepath.IsAbs(value) || strings.ContainsRune(value, '\x00') {
		return errors.New("path must be absolute")
	}
	info, err := os.Lstat(value)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("path is not a regular file")
	}
	return nil
}

func isolatedEnvironment(request Request, trustedDirectories []string) map[string]string {
	separator := ":"
	if request.Platform == "windows" {
		separator = ";"
	}
	environment := map[string]string{
		"HOME":            request.Home,
		"LANG":            request.Locale,
		"PATH":            strings.Join(trustedDirectories, separator),
		"TEMP":            request.TempRoot,
		"TMP":             request.TempRoot,
		"TMPDIR":          request.TempRoot,
		"XDG_CONFIG_HOME": request.ConfigRoot,
	}
	if len(request.ManagedAgentIdentities) > 0 {
		environment["MDS_RUNTIME_IDENTITIES"] = strings.Join(request.ManagedAgentIdentities, ",")
	}
	if request.Platform == "windows" {
		environment["USERPROFILE"] = request.Home
		environment["APPDATA"] = request.AppData
		environment["LOCALAPPDATA"] = request.LocalAppData
		environment["SystemRoot"] = request.SystemRoot
		environment["ComSpec"] = request.ComSpec
		pathExt := request.PathExt
		if pathExt == "" {
			pathExt = ".COM;.EXE;.BAT;.CMD"
		}
		environment["PATHEXT"] = pathExt
	}
	for key, value := range environment {
		if value == "" && key != "LANG" {
			delete(environment, key)
		}
	}
	return environment
}

type childEnvelope struct {
	Command  string       `json:"command"`
	State    string       `json:"state"`
	ExitCode int          `json:"exitCode"`
	Preview  childPreview `json:"preview"`
}

type childPreview struct {
	SchemaVersion   string            `json:"schemaVersion"`
	Kind            string            `json:"kind"`
	StateRoot       string            `json:"stateRoot"`
	ReceiptPath     string            `json:"receiptPath"`
	ProfileID       string            `json:"profileId"`
	CatalogRevision string            `json:"catalogRevision"`
	SelectedAgents  []string          `json:"selectedAgents"`
	Agents          []childAgent      `json:"agents"`
	Packages        []json.RawMessage `json:"packages"`
	Capabilities    []childCapability `json:"capabilities"`
	Addons          []childAddon      `json:"addons"`
	Preflights      []childPreflight  `json:"preflights"`
	OptionalGaps    []string          `json:"optionalGaps"`
	Blockers        []string          `json:"blockers"`
	Plan            *childPlan        `json:"plan"`
	Digest          *string           `json:"digest"`
	Readiness       string            `json:"readiness"`
	Remediation     string            `json:"remediation"`
	InstanceID      *string           `json:"instanceId"`
}

type childAgent struct {
	ID              string  `json:"id"`
	Command         string  `json:"command"`
	ExpectedVersion string  `json:"expectedVersion"`
	ExecutablePath  *string `json:"executablePath"`
	State           string  `json:"state"`
	Ownership       string  `json:"ownership"`
	Detail          string  `json:"detail"`
}

type childCapability struct {
	ID        string  `json:"id"`
	RuntimeID string  `json:"runtimeId"`
	State     string  `json:"state"`
	SourceID  string  `json:"sourceId"`
	Detail    *string `json:"detail,omitempty"`
}

type childAddon struct {
	AgentID     string `json:"agentId"`
	Detail      string `json:"detail"`
	Fingerprint string `json:"fingerprint"`
	ID          string `json:"id"`
	State       string `json:"state"`
	Version     string `json:"version"`
}

type childPreflight struct {
	ID       string  `json:"id"`
	Required bool    `json:"required"`
	Status   string  `json:"status"`
	Detail   *string `json:"detail,omitempty"`
}

type childPlan struct {
	Schema          string            `json:"$schema"`
	SchemaVersion   string            `json:"schemaVersion"`
	Kind            string            `json:"kind"`
	CatalogRevision string            `json:"catalogRevision"`
	DesiredState    childDesiredState `json:"desiredState"`
	Platform        childPlatform     `json:"platform"`
	ObservedState   json.RawMessage   `json:"observedState"`
	Preflights      []childPreflight  `json:"preflights"`
	Actions         []json.RawMessage `json:"actions"`
	Digest          string            `json:"digest"`
}

type childDesiredState struct {
	ProfileID      string            `json:"profileId"`
	SelectedAgents []string          `json:"selectedAgents"`
	RuntimeAddons  []json.RawMessage `json:"runtimeAddons,omitempty"`
}

type childPlatform struct {
	Arch string `json:"arch"`
	OS   string `json:"os"`
}

func decodeEnvelope(value string) (childEnvelope, error) {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	var envelope childEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return childEnvelope{}, &Error{Code: "invalid-json"}
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return childEnvelope{}, &Error{Code: "invalid-json"}
	}
	return envelope, nil
}

func validateEnvelope(
	envelope childEnvelope,
	processExitCode int,
	selectedAgents []string,
) (Result, error) {
	preview := envelope.Preview
	if envelope.Command != "setup" || envelope.ExitCode != processExitCode ||
		envelope.State != preview.Readiness ||
		preview.SchemaVersion != childSchemaVersion ||
		preview.Kind != "environment-preview" ||
		preview.ProfileID != childProfileID ||
		preview.InstanceID != nil ||
		!sha256Pattern.MatchString(preview.CatalogRevision) ||
		!equalStrings(preview.SelectedAgents, selectedAgents) ||
		len(preview.Packages) != 0 {
		return Result{}, &Error{Code: "contract"}
	}
	blockers, err := safeSummaryIDs(preview.Blockers)
	if err != nil {
		return Result{}, err
	}
	optionalGaps, err := safeSummaryIDs(preview.OptionalGaps)
	if err != nil {
		return Result{}, err
	}
	switch preview.Readiness {
	case "preview":
		if processExitCode != 2 || preview.Digest == nil ||
			!sha256Pattern.MatchString(*preview.Digest) || preview.Plan == nil ||
			len(blockers) != 0 {
			return Result{}, &Error{Code: "contract"}
		}
		if err := validatePlan(*preview.Plan, *preview.Digest, preview.CatalogRevision, selectedAgents); err != nil {
			return Result{}, err
		}
	case "blocked":
		if processExitCode != 3 || preview.Digest != nil || preview.Plan != nil ||
			len(blockers) == 0 {
			return Result{}, &Error{Code: "contract"}
		}
	default:
		return Result{}, &Error{Code: "contract"}
	}

	ownership, err := validateAgents(preview.Agents, selectedAgents, preview.Readiness)
	if err != nil {
		return Result{}, err
	}
	if err := validateCapabilities(preview.Capabilities, selectedAgents); err != nil {
		return Result{}, err
	}
	addons, err := validateAddons(preview.Addons, selectedAgents)
	if err != nil {
		return Result{}, err
	}
	configDigest, err := configurationDigest(preview, ownership, addons)
	if err != nil {
		return Result{}, &Error{Code: "contract"}
	}
	result := Result{
		SchemaVersion:   childSchemaVersion,
		CatalogRevision: preview.CatalogRevision,
		Readiness:       preview.Readiness,
		SelectedAgents:  append([]string(nil), selectedAgents...),
		Workflows:       append([]string(nil), exactWorkflows...),
		Addons:          addons,
		Ownership:       ownership,
		ConfigDigest:    configDigest,
		Blockers:        blockers,
		OptionalGapIDs:  optionalGaps,
	}
	if preview.Digest != nil {
		result.Digest = *preview.Digest
	}
	return result, nil
}

func validatePlan(
	plan childPlan,
	digest,
	catalogRevision string,
	selectedAgents []string,
) error {
	if plan.Schema != "../contracts/apply-plan.schema.json" ||
		plan.SchemaVersion != childSchemaVersion || plan.Kind != "apply-plan" ||
		plan.CatalogRevision != catalogRevision || plan.Digest != digest ||
		plan.DesiredState.ProfileID != childProfileID ||
		!equalStrings(plan.DesiredState.SelectedAgents, selectedAgents) ||
		len(plan.ObservedState) == 0 {
		return &Error{Code: "contract"}
	}
	return nil
}

func validateAgents(
	agents []childAgent,
	selected []string,
	readiness string,
) ([]Ownership, error) {
	if len(agents) != len(selected) {
		return nil, &Error{Code: "contract"}
	}
	byID := make(map[string]childAgent, len(agents))
	for _, agent := range agents {
		if _, duplicate := byID[agent.ID]; duplicate || !contains(selected, agent.ID) ||
			!contains([]string{"external", "managed", "none"}, agent.Ownership) ||
			!contains([]string{"ready", "installable", "unsupported", "drift"}, agent.State) {
			return nil, &Error{Code: "contract"}
		}
		if readiness == "preview" &&
			(agent.State != "ready" || agent.Ownership != "external" || agent.ExecutablePath == nil) {
			return nil, &Error{Code: "contract"}
		}
		byID[agent.ID] = agent
	}
	result := make([]Ownership, 0, len(selected))
	for _, id := range selected {
		agent, exists := byID[id]
		if !exists {
			return nil, &Error{Code: "contract"}
		}
		result = append(result, Ownership{
			ID: id, Ownership: agent.Ownership, State: agent.State,
		})
	}
	return result, nil
}

func validateCapabilities(capabilities []childCapability, selected []string) error {
	wanted := make(map[string]bool, len(selected)*len(exactWorkflows))
	for _, agent := range selected {
		for _, workflow := range exactWorkflows {
			wanted[agent+"\x00"+workflow] = true
		}
	}
	if len(capabilities) != len(wanted) {
		return &Error{Code: "contract"}
	}
	for _, capability := range capabilities {
		key := capability.RuntimeID + "\x00" + capability.ID
		if !wanted[key] {
			return &Error{Code: "contract"}
		}
		delete(wanted, key)
	}
	if len(wanted) != 0 {
		return &Error{Code: "contract"}
	}
	return nil
}

func validateAddons(addons []childAddon, selected []string) ([]Addon, error) {
	wanted := make(map[string]bool)
	for _, id := range selected {
		if id == "opencode" || id == "codex" {
			wanted[id] = true
		}
	}
	if len(addons) != len(wanted) {
		return nil, &Error{Code: "contract"}
	}
	result := make([]Addon, 0, len(addons))
	for _, addon := range addons {
		if !wanted[addon.AgentID] || addon.ID != "omo" || addon.Version != "4.19.2" ||
			!sha256Pattern.MatchString(addon.Fingerprint) ||
			!contains([]string{"conflict", "installable", "ready", "unverifiable"}, addon.State) {
			return nil, &Error{Code: "contract"}
		}
		delete(wanted, addon.AgentID)
		result = append(result, Addon{
			AgentID: addon.AgentID, ID: addon.ID, Version: addon.Version,
			State: addon.State, Fingerprint: addon.Fingerprint,
		})
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].AgentID < result[right].AgentID
	})
	return result, nil
}

func configurationDigest(
	preview childPreview,
	ownership []Ownership,
	addons []Addon,
) (string, error) {
	preflights := make([]struct {
		ID       string `json:"id"`
		Required bool   `json:"required"`
		Status   string `json:"status"`
	}, 0, len(preview.Preflights))
	for _, preflight := range preview.Preflights {
		if !summaryIDPattern.MatchString(preflight.ID) ||
			!contains([]string{"ready", "optional-gap", "unsupported", "unverifiable"}, preflight.Status) {
			return "", &Error{Code: "contract"}
		}
		preflights = append(preflights, struct {
			ID       string `json:"id"`
			Required bool   `json:"required"`
			Status   string `json:"status"`
		}{preflight.ID, preflight.Required, preflight.Status})
	}
	sort.Slice(preflights, func(left, right int) bool {
		return preflights[left].ID < preflights[right].ID
	})
	value := struct {
		Domain          string      `json:"domain"`
		ChildPlanDigest string      `json:"child_plan_digest"`
		Ownership       []Ownership `json:"ownership"`
		Addons          []Addon     `json:"addons"`
		Preflights      any         `json:"preflights"`
	}{
		Domain: "my-desk-setup/omh-native-config/v1",
		ChildPlanDigest: func() string {
			if preview.Digest == nil {
				return "blocked"
			}
			return *preview.Digest
		}(),
		Ownership:  ownership,
		Addons:     addons,
		Preflights: preflights,
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func safeSummaryIDs(values []string) ([]string, error) {
	result := append([]string(nil), values...)
	sort.Strings(result)
	for index, value := range result {
		if !summaryIDPattern.MatchString(value) ||
			(index > 0 && result[index-1] == value) {
			return nil, &Error{Code: "contract"}
		}
	}
	return result, nil
}

func sortedAgentIDs(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for id := range values {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func compact(values []string) []string {
	if len(values) == 0 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func equalStrings(left, right []string) bool {
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
