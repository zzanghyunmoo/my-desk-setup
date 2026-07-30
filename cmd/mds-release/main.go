package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/release"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stderr))
}

func run(ctx context.Context, arguments []string, stderr io.Writer) int {
	if len(arguments) == 0 {
		printUsage(stderr)
		return 2
	}
	var err error
	switch arguments[0] {
	case "build":
		err = runBuild(ctx, arguments[1:], stderr)
	case "verify":
		err = runVerify(arguments[1:], stderr)
	default:
		printUsage(stderr)
		return 2
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runBuild(ctx context.Context, arguments []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("mds-release build", flag.ContinueOnError)
	flags.SetOutput(stderr)
	source := flags.String("source", ".", "repository source root")
	output := flags.String("output", "dist", "new release output directory")
	version := flags.String("version", "", "release version without a v prefix")
	commit := flags.String("commit", "", "full release commit SHA")
	date := flags.String("date", "", "release timestamp in RFC3339 format")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("mds-release build does not accept positional arguments")
	}
	timestamp, err := time.Parse(time.RFC3339, *date)
	if err != nil {
		return fmt.Errorf("parse release date: %w", err)
	}
	return release.Build(ctx, release.Options{
		SourceRoot: *source,
		OutputDir:  *output,
		Version:    *version,
		Commit:     *commit,
		Date:       timestamp,
	})
}

func runVerify(arguments []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("mds-release verify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	directory := flags.String("directory", "dist", "release directory")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("mds-release verify does not accept positional arguments")
	}
	_, err := release.Verify(*directory)
	return err
}

func printUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(
		writer,
		"usage: mds-release <build|verify> [flags]",
	)
}
