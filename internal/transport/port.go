package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	DefaultTimeout     = 10 * time.Minute
	DefaultOutputLimit = 1 << 20
	DiagnosticLimit    = 2048
)

type Command struct {
	Executable       string
	Arguments        []string
	Stdin            []byte
	Environment      map[string]string
	EnvironmentMode  EnvironmentMode
	WorkingDirectory string
	Timeout          time.Duration
	OutputLimit      int
}

type EnvironmentMode string

const (
	// EnvironmentSafeInherited preserves the existing command contract: only
	// the reviewed ambient allowlist is inherited, then explicit values win.
	EnvironmentSafeInherited EnvironmentMode = ""
	// EnvironmentReplace starts from an empty environment and passes exactly
	// the validated entries supplied by the caller.
	EnvironmentReplace EnvironmentMode = "replace"
)

type Result struct {
	Executable string
	Arguments  []string
	Stdout     string
	Stderr     string
	ExitCode   int
}

type Port interface {
	Run(context.Context, Command) (Result, error)
}

type Executor struct{}

func (Executor) Run(
	ctx context.Context,
	executable string,
	arguments []string,
	stdin []byte,
	environment map[string]string,
	workingDirectory string,
	timeout time.Duration,
	outputLimit int,
) (Result, error) {
	return (Executor{}).RunCommand(ctx, Command{
		Executable:       executable,
		Arguments:        arguments,
		Stdin:            stdin,
		Environment:      environment,
		WorkingDirectory: workingDirectory,
		Timeout:          timeout,
		OutputLimit:      outputLimit,
	})
}

func (Executor) RunCommand(ctx context.Context, specification Command) (Result, error) {
	executable := specification.Executable
	arguments := specification.Arguments
	stdin := specification.Stdin
	environment := specification.Environment
	workingDirectory := specification.WorkingDirectory
	timeout := specification.Timeout
	outputLimit := specification.OutputLimit
	if executable == "" {
		return Result{}, errors.New("executable is required")
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if outputLimit <= 0 {
		outputLimit = DefaultOutputLimit
	}
	commandEnv, err := commandEnvironmentFor(
		environment,
		specification.EnvironmentMode,
	)
	if err != nil {
		return Result{}, fmt.Errorf("prepare command environment: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stdout := newLimitedBuffer(outputLimit)
	stderr := newLimitedBuffer(outputLimit)
	command := exec.CommandContext(ctx, executable, arguments...)
	tree, err := newProcessTree(command)
	if err != nil {
		return Result{}, fmt.Errorf("prepare command process tree: %w", err)
	}
	defer func() {
		_ = tree.Close()
	}()
	command.Cancel = tree.Terminate
	command.WaitDelay = 5 * time.Second
	command.Env = commandEnv
	command.Dir = workingDirectory
	if len(stdin) > 0 {
		command.Stdin = bytes.NewReader(stdin)
	}
	command.Stdout = stdout
	command.Stderr = stderr
	err = command.Start()
	if err == nil {
		if attachErr := tree.Attach(); attachErr != nil {
			_ = tree.Terminate()
			_ = command.Wait()
			err = fmt.Errorf("attach command process tree: %w", attachErr)
		} else {
			err = command.Wait()
		}
	}
	terminationErr := tree.Terminate()
	if terminationErr != nil && err == nil {
		err = fmt.Errorf("terminate command descendants: %w", terminationErr)
	}
	result := Result{
		Executable: executable,
		Arguments:  append([]string(nil), arguments...),
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		ExitCode:   0,
	}
	if ctx.Err() != nil {
		result.ExitCode = -1
		return result, fmt.Errorf("command timed out after %s: %w", timeout, ctx.Err())
	}
	if err == nil {
		return result, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		result.ExitCode = exitError.ExitCode()
		return result, &CommandError{Result: result}
	}
	result.ExitCode = -1
	return result, fmt.Errorf("run %s: %w", executable, err)
}

func commandEnvironment(overrides map[string]string) ([]string, error) {
	return commandEnvironmentFor(overrides, EnvironmentSafeInherited)
}

func commandEnvironmentFor(
	overrides map[string]string,
	mode EnvironmentMode,
) ([]string, error) {
	if mode != EnvironmentSafeInherited && mode != EnvironmentReplace {
		return nil, fmt.Errorf("unsupported command environment mode %q", mode)
	}
	values := make(map[string]string, len(safeInheritedEnvironmentKeys)+len(overrides))
	if mode == EnvironmentSafeInherited {
		for _, key := range safeInheritedEnvironmentKeys {
			if value, ok := os.LookupEnv(key); ok {
				values[key] = value
			}
		}
	}
	for key, value := range overrides {
		if err := validateEnvironmentEntry(key, value); err != nil {
			return nil, err
		}
		for inheritedKey := range values {
			if strings.EqualFold(inheritedKey, key) {
				delete(values, inheritedKey)
			}
		}
		values[key] = value
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	environment := make([]string, 0, len(keys))
	for _, key := range keys {
		environment = append(environment, key+"="+values[key])
	}
	// Sort the final entries, not only their keys. A key that is the prefix of
	// another key (for example ProgramFiles and ProgramFiles(x86)) changes
	// ordering after the '=' delimiter is appended.
	sort.Strings(environment)
	return environment, nil
}

var safeInheritedEnvironmentKeys = []string{
	"APPDATA",
	"ComSpec",
	"HOME",
	"LANG",
	"LC_ALL",
	"LC_CTYPE",
	"LOCALAPPDATA",
	"LOGNAME",
	"PATH",
	"PATHEXT",
	"ProgramData",
	"ProgramFiles",
	"ProgramFiles(x86)",
	"SHELL",
	"SystemRoot",
	"TEMP",
	"TERM",
	"TMP",
	"TMPDIR",
	"USER",
	"USERPROFILE",
	"XDG_CACHE_HOME",
	"XDG_CONFIG_HOME",
	"XDG_DATA_HOME",
	"XDG_STATE_HOME",
}

func validateEnvironmentEntry(key, value string) error {
	if key == "" || strings.ContainsAny(key, "=\x00") {
		return fmt.Errorf("invalid environment key %q", key)
	}
	if strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("environment value for %q contains NUL", key)
	}
	normalized := strings.ToUpper(key)
	normalized = strings.Map(func(character rune) rune {
		switch {
		case character >= 'A' && character <= 'Z':
			return character
		case character >= '0' && character <= '9':
			return character
		default:
			return -1
		}
	}, normalized)
	for _, marker := range []string{
		"APIKEY",
		"ACCESSKEY",
		"AUTHENTICATION",
		"AUTHORIZATION",
		"COOKIE",
		"CREDENTIAL",
		"PASSWORD",
		"PASSWD",
		"PRIVATEKEY",
		"SECRET",
		"SSHAUTHSOCK",
		"TOKEN",
	} {
		if strings.Contains(normalized, marker) {
			return fmt.Errorf("credential-shaped environment key %q is not allowed", key)
		}
	}
	return nil
}

func guestArgv(command Command) (string, []string) {
	if len(command.Environment) == 0 {
		return command.Executable, append([]string(nil), command.Arguments...)
	}
	keys := make([]string, 0, len(command.Environment))
	for key := range command.Environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	arguments := make([]string, 0, len(keys)+1+len(command.Arguments))
	for _, key := range keys {
		arguments = append(arguments, key+"="+command.Environment[key])
	}
	arguments = append(arguments, command.Executable)
	arguments = append(arguments, command.Arguments...)
	return "env", arguments
}

type CommandError struct {
	Result Result
}

func (err *CommandError) Error() string {
	detail := SanitizeDiagnostic(strings.TrimSpace(
		err.Result.Stderr + "\n" + err.Result.Stdout,
	))
	if detail == "" {
		return fmt.Sprintf(
			"%s exited with code %d",
			err.Result.Executable,
			err.Result.ExitCode,
		)
	}
	return fmt.Sprintf(
		"%s exited with code %d: %s",
		err.Result.Executable,
		err.Result.ExitCode,
		detail,
	)
}

var credentialAssignmentPattern = regexp.MustCompile(
	`(?i)\b(api[_-]?key|authorization|cookie|credential|password|passwd|private[_-]?key|secret|token)\s*[:=]\s*[^\s,;]+`,
)
var credentialTokenPattern = regexp.MustCompile(
	`(?i)\b(gh[pousr]_[A-Za-z0-9_]{8,}|sk-[A-Za-z0-9_-]{8,}|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,})\b`,
)
var diagnosticURLPattern = regexp.MustCompile(`https?://[^\s<>"']+`)

// SanitizeDiagnostic returns a bounded, single-line-safe diagnostic suitable
// for journals and receipts. Raw child-process output remains transient.
func SanitizeDiagnostic(value string) string {
	value = credentialAssignmentPattern.ReplaceAllString(value, "$1=[redacted]")
	value = credentialTokenPattern.ReplaceAllString(value, "[redacted]")
	value = diagnosticURLPattern.ReplaceAllStringFunc(value, sanitizeURL)
	value = strings.TrimSpace(value)
	if len(value) > DiagnosticLimit {
		value = value[:DiagnosticLimit] + " [diagnostic truncated]"
	}
	return value
}

func sanitizeURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return "[redacted-url]"
	}
	if parsed.User != nil {
		parsed.User = url.User("[redacted]")
	}
	query := parsed.Query()
	for key := range query {
		normalized := strings.ToLower(key)
		for _, marker := range []string{
			"auth", "code", "cookie", "credential", "key", "password", "secret", "signature", "token",
		} {
			if strings.Contains(normalized, marker) {
				query.Set(key, "[redacted]")
				break
			}
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	remaining int
	truncated bool
}

func newLimitedBuffer(limit int) *limitedBuffer {
	return &limitedBuffer{remaining: limit}
}

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	if buffer.remaining > 0 {
		toWrite := data
		if len(toWrite) > buffer.remaining {
			toWrite = toWrite[:buffer.remaining]
			buffer.truncated = true
		}
		_, _ = buffer.buffer.Write(toWrite)
		buffer.remaining -= len(toWrite)
	} else if len(data) > 0 {
		buffer.truncated = true
	}
	return originalLength, nil
}

func (buffer *limitedBuffer) String() string {
	if !buffer.truncated {
		return buffer.buffer.String()
	}
	return buffer.buffer.String() + "\n[output truncated]\n"
}
