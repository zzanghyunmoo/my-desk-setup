package main

import (
	"os"

	"github.com/zzanghyunmoo/my-desk-setup/internal/cli"
	"github.com/zzanghyunmoo/my-desk-setup/internal/shutdown"
)

func main() {
	ctx, stop := shutdown.Notify()
	code := cli.RunContext(
		ctx,
		os.Args[1:],
		cli.DefaultStreams(),
		cli.DefaultRuntime(),
	)
	stop()
	os.Exit(code)
}
