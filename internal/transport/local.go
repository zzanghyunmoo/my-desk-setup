package transport

import "context"

type Local struct {
	executor Executor
}

func NewLocal() Local {
	return Local{executor: Executor{}}
}

func (local Local) Run(ctx context.Context, command Command) (Result, error) {
	return local.executor.Run(
		ctx,
		command.Executable,
		command.Arguments,
		command.Stdin,
		command.Environment,
		command.WorkingDirectory,
		command.Timeout,
		command.OutputLimit,
	)
}
