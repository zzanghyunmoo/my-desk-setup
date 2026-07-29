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
	executable, arguments := WSLArgv(wsl.Distribution, command)
	return wsl.executor.Run(
		ctx,
		executable,
		arguments,
		command.Timeout,
		command.OutputLimit,
	)
}

func WSLArgv(distribution string, command Command) (string, []string) {
	arguments := []string{"--distribution", distribution, "--exec", command.Executable}
	arguments = append(arguments, command.Arguments...)
	return "wsl.exe", arguments
}
