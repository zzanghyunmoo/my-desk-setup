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
		command.Stdin,
		nil,
		"",
		command.Timeout,
		command.OutputLimit,
	)
}

func LimaArgv(instance string, command Command) (string, []string) {
	guestExecutable, guestArguments := guestArgv(command)
	arguments := []string{"shell", "--tty=false"}
	if command.WorkingDirectory != "" {
		arguments = append(arguments, "--workdir", command.WorkingDirectory)
	}
	arguments = append(arguments, instance, "--", guestExecutable)
	arguments = append(arguments, guestArguments...)
	return "limactl", arguments
}
