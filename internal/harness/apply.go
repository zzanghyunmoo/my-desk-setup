package harness

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

type ApplyResult struct {
	SchemaVersion      string      `json:"schema_version"`
	Status             string      `json:"status"`
	Digest             string      `json:"digest"`
	CatalogRevision    string      `json:"catalog_revision"`
	SelectedAgents     []string    `json:"selected_agents"`
	Workflows          []string    `json:"workflows"`
	Addons             []Addon     `json:"addons"`
	Ownership          []Ownership `json:"ownership"`
	ConfigDigest       string      `json:"config_digest"`
	CompletedActionIDs []string    `json:"completed_action_ids"`
}

type childApplyEnvelope struct {
	Apply    childApply   `json:"apply"`
	Command  string       `json:"command"`
	ExitCode int          `json:"exitCode"`
	Preview  childPreview `json:"preview"`
	State    string       `json:"state"`
}

type childApply struct {
	Status             string   `json:"status"`
	CompletedActionIDs []string `json:"completedActionIds"`
	Failure            *string  `json:"failure,omitempty"`
}

// Apply executes one exact child invocation. It intentionally does not run a
// second preview; the approved digest is supplied to OMH's own stale-plan gate.
func (runner Runner) Apply(
	ctx context.Context,
	request Request,
	expectedDigest string,
) (ApplyResult, error) {
	if !sha256Pattern.MatchString(expectedDigest) {
		return ApplyResult{}, &Error{Code: "expected-digest"}
	}
	if runner.Port == nil {
		return ApplyResult{}, &Error{Code: "transport"}
	}
	agents, trustedDirectories, err := validateRequest(request)
	if err != nil {
		return ApplyResult{}, err
	}
	timeout := request.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	if timeout < 0 || timeout > maximumTimeout {
		return ApplyResult{}, &Error{Code: "timeout-contract"}
	}
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
			"--apply",
			"--digest",
			expectedDigest,
		},
		Environment:      isolatedEnvironment(request, trustedDirectories),
		EnvironmentMode:  transport.EnvironmentReplace,
		WorkingDirectory: request.TempRoot,
		Timeout:          timeout,
		OutputLimit:      childOutputLimit,
	}
	processResult, runErr := runner.Port.Run(ctx, command)
	if errors.Is(runErr, context.DeadlineExceeded) ||
		(runErr != nil && strings.Contains(runErr.Error(), "timed out")) {
		return ApplyResult{}, &Error{Code: "timeout"}
	}
	switch processResult.ExitCode {
	case 4:
		return ApplyResult{}, &Error{Code: "stale-preview"}
	case 5:
		return ApplyResult{}, &Error{Code: "partial-unready"}
	}
	if runErr != nil || processResult.ExitCode != 0 {
		return ApplyResult{}, &Error{Code: "process"}
	}
	if strings.Contains(processResult.Stdout, "[output truncated]") ||
		strings.Contains(processResult.Stderr, "[output truncated]") ||
		len(processResult.Stdout) > childOutputLimit {
		return ApplyResult{}, &Error{Code: "output-limit"}
	}
	envelope, err := decodeApplyEnvelope(processResult.Stdout)
	if err != nil {
		return ApplyResult{}, err
	}
	if envelope.Command != "setup" || envelope.ExitCode != 0 ||
		envelope.State != "ready" || envelope.Apply.Status != "ready" ||
		envelope.Apply.Failure != nil {
		return ApplyResult{}, &Error{Code: "contract"}
	}
	preview, err := validateEnvelope(childEnvelope{
		Command: "setup", State: envelope.Preview.Readiness,
		ExitCode: 2, Preview: envelope.Preview,
	}, 2, agents)
	if err != nil {
		return ApplyResult{}, err
	}
	if preview.Digest != expectedDigest {
		return ApplyResult{}, &Error{Code: "digest-mismatch"}
	}
	completed, err := safeSummaryIDs(envelope.Apply.CompletedActionIDs)
	if err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{
		SchemaVersion:      preview.SchemaVersion,
		Status:             "ready",
		Digest:             preview.Digest,
		CatalogRevision:    preview.CatalogRevision,
		SelectedAgents:     append([]string{}, preview.SelectedAgents...),
		Workflows:          append([]string{}, preview.Workflows...),
		Addons:             append([]Addon{}, preview.Addons...),
		Ownership:          append([]Ownership{}, preview.Ownership...),
		ConfigDigest:       preview.ConfigDigest,
		CompletedActionIDs: append([]string{}, completed...),
	}, nil
}

func decodeApplyEnvelope(value string) (childApplyEnvelope, error) {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	var envelope childApplyEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return childApplyEnvelope{}, &Error{Code: "invalid-json"}
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return childApplyEnvelope{}, &Error{Code: "invalid-json"}
	}
	return envelope, nil
}
