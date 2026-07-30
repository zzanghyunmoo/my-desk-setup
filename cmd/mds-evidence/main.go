package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/zzanghyunmoo/my-desk-setup/internal/evidence"
	"github.com/zzanghyunmoo/my-desk-setup/internal/output"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(arguments []string, stdout, stderr io.Writer) int {
	command := newRoot(stdout)
	command.SetArgs(arguments)
	command.SetErr(stderr)
	if err := command.Execute(); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func newRoot(stdout io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "mds-evidence",
		Short:         "Capture and verify secret-free actual target evidence",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.AddCommand(newCertifyCommand(stdout))
	root.AddCommand(newVerifyCommand(stdout))
	return root
}

func newCertifyCommand(stdout io.Writer) *cobra.Command {
	var request evidence.CertifyRequest
	command := &cobra.Command{
		Use:   "certify",
		Short: "Run read-only plan and doctor with a production mds binary",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			request.Now = time.Now
			manifest, err := evidence.Certify(command.Context(), request)
			if err != nil {
				return err
			}
			if err := writeJSON(stdout, manifest); err != nil {
				return err
			}
			if manifest.Status != evidence.StatusVerified {
				return fmt.Errorf(
					"actual target certification is %s; evidence was preserved at %s",
					manifest.Status,
					request.OutputDir,
				)
			}
			return nil
		},
	}
	flags := command.Flags()
	flags.StringVar(&request.MDSPath, "mds", "", "absolute production mds binary path")
	flags.StringVar(&request.TargetID, "target", "", "explicit actual target ID")
	flags.StringVar(&request.OutputDir, "output", "", "new evidence bundle directory")
	flags.BoolVar(&request.All, "all", false, "certify every target-eligible component")
	flags.StringVar(&request.Profile, "profile", "", "certify a named profile")
	flags.StringSliceVar(
		&request.Components,
		"component",
		nil,
		"certify a component or capability",
	)
	_ = command.MarkFlagRequired("mds")
	_ = command.MarkFlagRequired("target")
	_ = command.MarkFlagRequired("output")
	return command
}

func newVerifyCommand(stdout io.Writer) *cobra.Command {
	var bundle string
	var options evidence.VerifyOptions
	var requireVerified bool
	command := &cobra.Command{
		Use:   "verify",
		Short: "Strictly verify an actual target evidence bundle",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if requireVerified &&
				(options.ExpectedCLIRevision == "" ||
					options.ExpectedCatalogRevision == "" ||
					options.ExpectedPlanDigest == "" ||
					options.ExpectedTargetID == "") {
				return errors.New(
					"--require-verified also requires expected CLI, catalog, plan digest, and target",
				)
			}
			if options.MaxAge > 0 {
				options.Now = time.Now().UTC()
			}
			manifest, err := evidence.Verify(bundle, options)
			if err != nil {
				return err
			}
			if err := writeJSON(stdout, manifest); err != nil {
				return err
			}
			if requireVerified && manifest.Status != evidence.StatusVerified {
				return fmt.Errorf(
					"actual target evidence status is %s, not verified",
					manifest.Status,
				)
			}
			return nil
		},
	}
	flags := command.Flags()
	flags.StringVar(&bundle, "bundle", "", "evidence bundle directory")
	flags.StringVar(
		&options.ExpectedCLIRevision,
		"expected-cli-revision",
		"",
		"release CLI revision expected by the publication lane",
	)
	flags.StringVar(
		&options.ExpectedCatalogRevision,
		"expected-catalog-revision",
		"",
		"catalog revision expected by the publication lane",
	)
	flags.StringVar(
		&options.ExpectedPlanDigest,
		"expected-plan-digest",
		"",
		"reviewed plan digest expected by the publication lane",
	)
	flags.StringVar(
		&options.ExpectedTargetID,
		"expected-target",
		"",
		"actual target ID expected by the publication lane",
	)
	flags.DurationVar(
		&options.MaxAge,
		"max-age",
		0,
		"optional maximum evidence age, for example 24h",
	)
	flags.BoolVar(
		&requireVerified,
		"require-verified",
		false,
		"fail unless recomputed actual target status is verified",
	)
	_ = command.MarkFlagRequired("bundle")
	return command
}

func writeJSON(writer io.Writer, value any) error {
	if writer == nil {
		return errors.New("output writer is required")
	}
	return output.JSON(writer, value)
}
