package transport

import (
	"context"
	"errors"
	"strings"
)

type WSL struct {
	Distribution string
	executor     Executor
}

func NewWSL(distribution string) (WSL, error) {
	distribution = strings.TrimSpace(distribution)
	if distribution == "" || strings.ContainsAny(distribution, "\r\n\x00") {
		return WSL{}, errors.New("valid WSL distribution is required")
	}
	return WSL{Distribution: distribution, executor: Executor{}}, nil
}

func (wsl WSL) Run(ctx context.Context, command Command) (Result, error) {
	if err := ValidateCommand(command); err != nil {
		return Result{}, err
	}
	executable, arguments := WSLArgv(wsl.Distribution, command)
	return wsl.executor.Run(
		ctx,
		executable,
		arguments,
		command.Stdin,
		nil,
		"",
		command.Timeout,
		command.OutputLimit,
	)
}

func WSLArgv(distribution string, command Command) (string, []string) {
	guestExecutable, guestArguments := guestArgv(command)
	arguments := []string{"--distribution", distribution}
	if command.WorkingDirectory != "" {
		arguments = append(arguments, "--cd", command.WorkingDirectory)
	}
	arguments = append(arguments, "--exec", guestExecutable)
	arguments = append(arguments, guestArguments...)
	return "wsl.exe", arguments
}
