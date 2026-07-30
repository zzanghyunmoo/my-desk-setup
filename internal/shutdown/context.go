package shutdown

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// Notify returns a process context canceled by the first interrupt or
// termination signal. Once canceled it restores the default signal behavior,
// so a second signal remains an immediate forced-exit path.
func Notify() (context.Context, func()) {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	go func() {
		<-ctx.Done()
		stop()
	}()
	return ctx, stop
}
