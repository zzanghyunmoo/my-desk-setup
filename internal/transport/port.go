package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

const (
	DefaultTimeout     = 2 * time.Minute
	DefaultOutputLimit = 1 << 20
)

type Command struct {
	Executable  string
	Arguments   []string
	Timeout     time.Duration
	OutputLimit int
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

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stdout := newLimitedBuffer(outputLimit)
	stderr := newLimitedBuffer(outputLimit)
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
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
