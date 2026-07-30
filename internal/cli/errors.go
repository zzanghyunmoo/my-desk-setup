package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zzanghyunmoo/my-desk-setup/internal/output"
)

const ErrorSchema = "mds.error/v1"

const (
	ExitSuccess        = 0
	ExitInternal       = 1
	ExitInvalidInput   = 2
	ExitStalePlan      = 3
	ExitActionRequired = 4
	ExitUnreachable    = 5
)

type ErrorEnvelope struct {
	SchemaVersion string       `json:"schema_version"`
	Status        string       `json:"status"`
	Code          string       `json:"code"`
	Message       string       `json:"message"`
	RecoveryHint  string       `json:"recovery_hint,omitempty"`
	Details       ErrorDetails `json:"details,omitempty"`
}

type ErrorDetails struct {
	Cause string `json:"cause,omitempty"`
}

type errorClass struct {
	code         string
	status       string
	message      string
	recoveryHint string
	exitCode     int
}

var (
	invalidInputClass = errorClass{
		code:         "invalid-input",
		status:       "error",
		message:      "command input is invalid",
		recoveryHint: "Correct the command arguments and retry.",
		exitCode:     ExitInvalidInput,
	}
	stalePlanClass = errorClass{
		code:         "stale-plan",
		status:       "stale",
		message:      "reviewed plan is stale",
		recoveryHint: "Run mds plan again, review the new digest, and retry.",
		exitCode:     ExitStalePlan,
	}
	actionRequiredClass = errorClass{
		code:         "action-required",
		status:       "action-required",
		message:      "command requires user action",
		recoveryHint: "Inspect the structured receipt or report, complete the indicated action, and retry.",
		exitCode:     ExitActionRequired,
	}
	unreachableClass = errorClass{
		code:         "unreachable",
		status:       "unreachable",
		message:      "target is unreachable",
		recoveryHint: "Make the selected target reachable and retry.",
		exitCode:     ExitUnreachable,
	}
	internalClass = errorClass{
		code:         "internal",
		status:       "error",
		message:      "internal command failure",
		recoveryHint: "Retry once; if the failure persists, inspect details and report the issue.",
		exitCode:     ExitInternal,
	}
)

type commandError struct {
	class errorClass
	cause error
}

func (err *commandError) Error() string {
	if err == nil {
		return ""
	}
	if err.cause != nil {
		return err.cause.Error()
	}
	return err.class.message
}

func (err *commandError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

func invalidInput(err error) error {
	return classifiedError(invalidInputClass, err)
}

func stalePlan(err error) error {
	return classifiedError(stalePlanClass, err)
}

func actionRequired(err error) error {
	return classifiedError(actionRequiredClass, err)
}

func unreachable(err error) error {
	return classifiedError(unreachableClass, err)
}

func classifiedError(class errorClass, err error) error {
	var existing *commandError
	if errors.As(err, &existing) {
		return err
	}
	return &commandError{class: class, cause: err}
}

func classifyError(err error) *commandError {
	var classified *commandError
	if errors.As(err, &classified) {
		return classified
	}
	return &commandError{class: internalClass, cause: err}
}

func (err *commandError) envelope() ErrorEnvelope {
	details := ErrorDetails{}
	if err.cause != nil {
		details.Cause = err.cause.Error()
	}
	return ErrorEnvelope{
		SchemaVersion: ErrorSchema,
		Status:        err.class.status,
		Code:          err.class.code,
		Message:       err.class.message,
		RecoveryHint:  err.class.recoveryHint,
		Details:       details,
	}
}

func writeCommandError(writer io.Writer, err error) int {
	classified := classifyError(err)
	if writer != nil {
		if writeErr := output.JSON(writer, classified.envelope()); writeErr != nil {
			return ExitInternal
		}
	}
	return classified.class.exitCode
}

func requestedJSON(arguments []string) bool {
	format := ""
	for index := 0; index < len(arguments); index++ {
		switch {
		case arguments[index] == "--":
			return format == "json"
		case arguments[index] == "--format" && index+1 < len(arguments):
			index++
			format = arguments[index]
		case strings.HasPrefix(arguments[index], "--format="):
			format = strings.TrimPrefix(arguments[index], "--format=")
		}
	}
	return format == "json"
}

func rejectFlagError(command *cobra.Command, err error) error {
	return invalidInput(fmt.Errorf("%s: %w", command.CommandPath(), err))
}

func noPositionalArgs(command *cobra.Command, arguments []string) error {
	if len(arguments) == 0 {
		return nil
	}
	return invalidInput(fmt.Errorf(
		"%s does not accept positional arguments: %q",
		command.CommandPath(),
		arguments,
	))
}
