package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const (
	DefaultTimeout     = 2 * time.Minute
	DefaultOutputLimit = 1 << 20
)

type Command struct {
	Executable       string
	Arguments        []string
	Environment      map[string]string
	WorkingDirectory string
	Timeout          time.Duration
	OutputLimit      int
}

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
	environment map[string]string,
	workingDirectory string,
	timeout time.Duration,
	outputLimit int,
) (Result, error) {
	if executable == "" {
		return Result{}, errors.New("executable is required")
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if outputLimit <= 0 {
		outputLimit = DefaultOutputLimit
	}
	commandEnv, err := commandEnvironment(environment)
	if err != nil {
		return Result{}, fmt.Errorf("prepare command environment: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stdout := newLimitedBuffer(outputLimit)
	stderr := newLimitedBuffer(outputLimit)
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Env = commandEnv
	command.Dir = workingDirectory
	command.Stdout = stdout
	command.Stderr = stderr
	err = command.Run()
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
	values := make(map[string]string, len(safeInheritedEnvironmentKeys)+len(overrides))
	for _, key := range safeInheritedEnvironmentKeys {
		if value, ok := os.LookupEnv(key); ok {
			values[key] = value
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
	return fmt.Sprintf("%s exited with code %d", err.Result.Executable, err.Result.ExitCode)
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
