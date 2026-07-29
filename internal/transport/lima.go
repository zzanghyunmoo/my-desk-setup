package transport

import (
	"context"
	"errors"
	"strings"
)

type Lima struct {
	Instance string
	executor Executor
}

func NewLima(instance string) (Lima, error) {
	instance = strings.TrimSpace(instance)
	if instance == "" || strings.ContainsAny(instance, "\r\n\x00") {
		return Lima{}, errors.New("valid Lima instance is required")
	}
	return Lima{Instance: instance, executor: Executor{}}, nil
}

func (lima Lima) Run(ctx context.Context, command Command) (Result, error) {
	executable, arguments := LimaArgv(lima.Instance, command)
	return lima.executor.Run(
		ctx,
		executable,
		arguments,
		command.Timeout,
		command.OutputLimit,
	)
}

func LimaArgv(instance string, command Command) (string, []string) {
	arguments := []string{"shell", instance, "--", command.Executable}
	arguments = append(arguments, command.Arguments...)
	return "limactl", arguments
}
