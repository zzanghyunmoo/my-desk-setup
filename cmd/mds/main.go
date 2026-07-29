package main

import (
	"os"

	"github.com/zzanghyunmoo/my-desk-setup/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], cli.DefaultStreams(), cli.DefaultRuntime()))
}
