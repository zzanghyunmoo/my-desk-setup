package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/zzanghyunmoo/my-desk-setup/internal/evidence"
	"github.com/zzanghyunmoo/my-desk-setup/internal/output"
	"github.com/zzanghyunmoo/my-desk-setup/internal/shutdown"
)

func main() {
	ctx, stop := shutdown.Notify()
	code := runContext(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(arguments []string, stdout, stderr io.Writer) int {
	return runContext(context.Background(), arguments, stdout, stderr)
}

func runContext(
	ctx context.Context,
	arguments []string,
	stdout,
	stderr io.Writer,
) int {
	command := newRoot(stdout)
	command.SetArgs(arguments)
	command.SetErr(stderr)
	if err := command.ExecuteContext(ctx); err != nil {
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
	root.AddCommand(newPrepareCommand(stdout))
	root.AddCommand(newCertifyCommand(stdout))
	root.AddCommand(newVerifyCommand(stdout))
	return root
}

func newPrepareCommand(stdout io.Writer) *cobra.Command {
	var request evidence.PrepareRequest
	command := &cobra.Command{
		Use:   "prepare",
		Short: "Derive the exact read-only certification identity",
		Long: "Derive the exact target identity, production binary identity, catalog revision, and plan digest without applying changes. " +
			"Review this bounded JSON before dispatching actual certification.",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			preparation, err := evidence.Prepare(command.Context(), request)
			if err != nil {
				return err
			}
			return writeJSON(stdout, preparation)
		},
	}
	flags := command.Flags()
	flags.StringVar(&request.MDSPath, "mds", "", "absolute production mds binary path")
	flags.StringVar(&request.TargetID, "target", "", "explicit actual target ID")
	flags.StringVar(
		&request.ExpectedBinarySHA256,
		"expected-binary-sha256",
		"",
		"SHA-256 of the exact release mds binary being prepared",
	)
	flags.BoolVar(&request.All, "all", false, "prepare every target-eligible component")
	flags.StringVar(&request.Profile, "profile", "", "prepare a named profile")
	flags.StringSliceVar(
		&request.Components,
		"component",
		nil,
		"prepare a component or capability",
	)
	_ = command.MarkFlagRequired("mds")
	_ = command.MarkFlagRequired("target")
	_ = command.MarkFlagRequired("expected-binary-sha256")
	return command
}

func newCertifyCommand(stdout io.Writer) *cobra.Command {
	var request evidence.CertifyRequest
	command := &cobra.Command{
		Use:   "certify",
		Short: "Plan, apply, repeat, and diagnose with a production mds binary",
		Long: "Plan, apply, repeat, and diagnose an actual target with a production mds binary. " +
			"Certification mutates the selected target using the reviewed plan digest, " +
			"but it never performs authentication.",
		Args: cobra.NoArgs,
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
	flags.StringVar(
		&request.Cohort,
		"cohort",
		"",
		"immutable certification cohort shared by all four targets",
	)
	flags.StringVar(
		&request.ExpectedBinarySHA256,
		"expected-binary-sha256",
		"",
		"SHA-256 of the exact release mds binary being certified",
	)
	flags.StringVar(
		&request.ExpectedPlanDigest,
		"expected-plan-digest",
		"",
		"reviewed plan digest that must match before apply",
	)
	flags.StringVar(
		&request.ExpectedGuestCreationNonceCommitment,
		"expected-guest-creation-nonce-commitment",
		"",
		"host-reviewed guest creation nonce commitment; required only for guest targets",
	)
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
	_ = command.MarkFlagRequired("cohort")
	_ = command.MarkFlagRequired("expected-binary-sha256")
	_ = command.MarkFlagRequired("expected-plan-digest")
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
					options.ExpectedTargetID == "" ||
					options.ExpectedBinarySHA256 == "" ||
					options.ExpectedCohort == "") {
				return errors.New(
					"strict publication verification requires expected CLI, catalog, plan digest, target, binary SHA-256, and cohort",
				)
			}
			options.RequireVerified = requireVerified
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
	flags.StringVar(
		&options.ExpectedBinarySHA256,
		"expected-binary-sha256",
		"",
		"exact release binary SHA-256 expected by the publication lane",
	)
	flags.StringVar(
		&options.ExpectedCohort,
		"expected-cohort",
		"",
		"immutable certification cohort expected by the publication lane",
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
