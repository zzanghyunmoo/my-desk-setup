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
	"github.com/zzanghyunmoo/my-desk-setup/internal/shutdown"
)

func main() {
	ctx, stop := shutdown.Notify()
	code := run(ctx, os.Args[1:], os.Stderr)
	stop()
	os.Exit(code)
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
	case "promote":
		err = runPromote(arguments[1:], stderr)
	case "verify-promotion":
		err = runVerifyPromotion(arguments[1:], stderr)
	case "extract-evidence":
		err = runExtractEvidence(arguments[1:], stderr)
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

func runPromote(arguments []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("mds-release promote", flag.ContinueOnError)
	flags.SetOutput(stderr)
	directory := flags.String(
		"directory",
		"release-dist",
		"verified release directory",
	)
	evidenceRoot := flags.String(
		"evidence-root",
		"",
		"directory containing exactly four actual-target evidence bundles",
	)
	commit := flags.String(
		"commit",
		"",
		"exact full release commit SHA",
	)
	cohort := flags.String(
		"cohort",
		"",
		"immutable certification cohort shared by all four targets",
	)
	maxAge := flags.Duration(
		"max-age",
		24*time.Hour,
		"maximum accepted target evidence age",
	)
	reportPath := flags.String(
		"report",
		"",
		"new deterministic promotion report path",
	)
	evidenceArchiveDir := flags.String(
		"evidence-archive-directory",
		"",
		"new directory for durable target evidence archives",
	)
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("mds-release promote does not accept positional arguments")
	}
	if *evidenceRoot == "" || *commit == "" || *cohort == "" ||
		*reportPath == "" || *evidenceArchiveDir == "" {
		return errors.New(
			"mds-release promote requires --evidence-root, --evidence-archive-directory, --commit, --cohort, and --report",
		)
	}
	if _, err := os.Lstat(*reportPath); err == nil {
		return fmt.Errorf("promotion report already exists: %s", *reportPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect promotion report path: %w", err)
	}
	report, err := release.Promote(release.PromotionOptions{
		ReleaseDir: *directory, EvidenceRoot: *evidenceRoot,
		ExpectedCommit: *commit, ExpectedCohort: *cohort,
		EvidenceArchiveDir: *evidenceArchiveDir,
		Now:                time.Now().UTC(), MaxAge: *maxAge,
	})
	if err != nil {
		return err
	}
	return release.WritePromotionReport(*reportPath, report)
}

func runVerifyPromotion(arguments []string, stderr io.Writer) error {
	flags := flag.NewFlagSet(
		"mds-release verify-promotion",
		flag.ContinueOnError,
	)
	flags.SetOutput(stderr)
	directory := flags.String(
		"directory",
		"release-dist",
		"verified release directory",
	)
	report := flags.String(
		"report",
		"release-promotion.json",
		"promotion report to verify",
	)
	evidenceArchiveDir := flags.String(
		"evidence-archive-directory",
		"",
		"directory containing durable target evidence archives",
	)
	commit := flags.String(
		"commit",
		"",
		"exact full release commit SHA",
	)
	cohort := flags.String(
		"cohort",
		"",
		"immutable certification cohort bound to the release report",
	)
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New(
			"mds-release verify-promotion does not accept positional arguments",
		)
	}
	if *commit == "" || *cohort == "" || *evidenceArchiveDir == "" {
		return errors.New(
			"mds-release verify-promotion requires --evidence-archive-directory, --commit, and --cohort",
		)
	}
	_, err := release.VerifyPromotionReport(
		*directory,
		*report,
		*evidenceArchiveDir,
		*commit,
		*cohort,
	)
	return err
}

func runExtractEvidence(arguments []string, stderr io.Writer) error {
	flags := flag.NewFlagSet(
		"mds-release extract-evidence",
		flag.ContinueOnError,
	)
	flags.SetOutput(stderr)
	archive := flags.String(
		"archive",
		"",
		"bounded GitHub Actions target-evidence ZIP",
	)
	output := flags.String(
		"output",
		"",
		"new directory for the exact evidence bundle",
	)
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New(
			"mds-release extract-evidence does not accept positional arguments",
		)
	}
	if *archive == "" || *output == "" {
		return errors.New(
			"mds-release extract-evidence requires --archive and --output",
		)
	}
	return release.ExtractEvidenceArtifact(*archive, *output)
}

func printUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(
		writer,
		"usage: mds-release <build|verify|promote|verify-promotion|extract-evidence> [flags]",
	)
}
