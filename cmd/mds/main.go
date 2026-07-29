package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/zzanghyunmoo/my-desk-setup/internal/version"
)

const usage = `my-desk-setup (mds)

Usage:
  mds --version
  mds version
  mds help

The plan, apply, doctor, and update commands are introduced by ZZA-100.
Authentication remains a manual user action.
`

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("mds", flag.ContinueOnError)
	flags.SetOutput(stderr)
	showVersion := flags.Bool("version", false, "print version information")
	flags.Usage = func() {
		_, _ = fmt.Fprint(stderr, usage)
	}

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		_, _ = fmt.Fprintln(stdout, version.String())
		return 0
	}

	switch {
	case flags.NArg() == 0:
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	case flags.NArg() == 1 && flags.Arg(0) == "version":
		_, _ = fmt.Fprintln(stdout, version.String())
		return 0
	case flags.NArg() == 1 && flags.Arg(0) == "help":
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command: %s\n\n%s", flags.Arg(0), usage)
		return 2
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
