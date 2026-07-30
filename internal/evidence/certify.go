package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/cli"
	"github.com/zzanghyunmoo/my-desk-setup/internal/doctor"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

var (
	cliRevisionPattern = regexp.MustCompile(
		`^([^\s()]+) \(commit=([^,\s()]+), date=([^)]+)\)$`,
	)
	cliVersionPattern = regexp.MustCompile(
		`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$`,
	)
	cliCommitPattern = regexp.MustCompile(
		`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`,
	)
)

func Certify(ctx context.Context, request CertifyRequest) (Manifest, error) {
	if err := validateCertifyRequest(request); err != nil {
		return Manifest{}, err
	}
	binarySHA256, err := hashRegularFile(request.MDSPath)
	if err != nil {
		return Manifest{}, err
	}
	if request.ExpectedBinarySHA256 != "" &&
		binarySHA256 != request.ExpectedBinarySHA256 {
		return Manifest{}, fmt.Errorf(
			"mds binary checksum mismatch: got=%s expected=%s",
			binarySHA256,
			request.ExpectedBinarySHA256,
		)
	}
	cli, err := readCLIIdentity(ctx, request.MDSPath)
	if err != nil {
		return Manifest{}, err
	}
	selectionArgs, err := selectionArguments(request)
	if err != nil {
		return Manifest{}, err
	}
	common := append(
		[]string{"--target", request.TargetID, "--format", "json"},
		selectionArgs...,
	)

	planOutput, _, err := runMDS(
		ctx,
		request.MDSPath,
		append([]string{"plan"}, common...),
		false,
	)
	if err != nil {
		return Manifest{}, fmt.Errorf("capture read-only plan: %w", err)
	}
	var plan planning.Plan
	if err := decodeStrict(planOutput, &plan); err != nil {
		return Manifest{}, fmt.Errorf("decode plan output: %w", err)
	}

	doctorOutput, doctorActionRequired, err := runMDS(
		ctx,
		request.MDSPath,
		append([]string{"doctor"}, common...),
		true,
	)
	if err != nil {
		return Manifest{}, fmt.Errorf("capture read-only doctor: %w", err)
	}
	var report doctor.Report
	if err := decodeStrict(doctorOutput, &report); err != nil {
		return Manifest{}, fmt.Errorf("decode doctor output: %w", err)
	}
	if err := validateDoctorExit(doctorActionRequired, report.Ready); err != nil {
		return Manifest{}, err
	}

	targetIdentity, err := targetIdentity(plan.Target)
	if err != nil {
		return Manifest{}, err
	}
	snapshot, err := doctorSnapshot(report)
	if err != nil {
		return Manifest{}, err
	}
	components, err := validatePlanDoctor(plan, snapshot, cli, request.TargetID)
	if err != nil {
		return Manifest{}, err
	}
	if err := scanEvidenceMaterial(PlanFile, planOutput); err != nil {
		return Manifest{}, err
	}
	if err := scanEvidenceMaterial(DoctorFile, doctorOutput); err != nil {
		return Manifest{}, err
	}
	finalBinarySHA256, err := hashRegularFile(request.MDSPath)
	if err != nil {
		return Manifest{}, err
	}
	if finalBinarySHA256 != binarySHA256 {
		return Manifest{}, errors.New(
			"mds binary changed while actual target evidence was being captured",
		)
	}

	status := StatusVerified
	if len(plan.Blockers) > 0 ||
		!snapshot.Ready ||
		!targetIdentityComplete(plan.Target) {
		status = StatusBlocked
	}
	now := time.Now().UTC()
	if request.Now != nil {
		now = request.Now().UTC()
	}
	manifest := Manifest{
		SchemaVersion: SchemaVersion, CaptureKind: CaptureKindActualTarget,
		Status:         status,
		CapturedAtUnix: json.Number(strconv.FormatInt(now.Unix(), 10)),
		Target:         targetIdentity, CLI: cli,
		BinarySHA256:    binarySHA256,
		CatalogRevision: plan.CatalogRevision, PlanDigest: plan.Digest,
		Components: components,
	}
	if err := writeBundle(request.OutputDir, manifest, plan, snapshot); err != nil {
		return Manifest{}, err
	}
	if _, err := Verify(request.OutputDir, VerifyOptions{
		ExpectedCLIRevision:     cli.Revision,
		ExpectedCatalogRevision: plan.CatalogRevision,
		ExpectedPlanDigest:      plan.Digest,
		ExpectedTargetID:        request.TargetID,
		ExpectedBinarySHA256:    binarySHA256,
	}); err != nil {
		return Manifest{}, fmt.Errorf("verify captured evidence: %w", err)
	}
	return manifest, nil
}

func validateCertifyRequest(request CertifyRequest) error {
	if request.MDSPath == "" {
		return errors.New("mds binary path is required")
	}
	if !filepath.IsAbs(request.MDSPath) {
		return errors.New("mds binary path must be absolute")
	}
	info, err := os.Lstat(request.MDSPath)
	if err != nil {
		return fmt.Errorf("inspect mds binary: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("mds binary must not be a symlink")
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return errors.New("mds binary must be a regular file")
	}
	if reparse, err := isReparsePoint(request.MDSPath); err != nil {
		return fmt.Errorf("inspect mds binary reparse state: %w", err)
	} else if reparse {
		return errors.New("mds binary must not be a reparse point")
	}
	if request.ExpectedBinarySHA256 != "" &&
		!sha256Pattern.MatchString(request.ExpectedBinarySHA256) {
		return errors.New("expected mds binary SHA-256 must be 64 lowercase hex characters")
	}
	if _, err := target.ParseID(request.TargetID); err != nil {
		return fmt.Errorf("invalid certification target: %w", err)
	}
	if request.OutputDir == "" {
		return errors.New("evidence output directory is required")
	}
	if _, err := os.Lstat(request.OutputDir); err == nil {
		return errors.New("evidence output directory already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect evidence output directory: %w", err)
	}
	return nil
}

func hashRegularFile(path string) (digest string, returnErr error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open mds binary for hashing: %w", err)
	}
	defer func() {
		if err := file.Close(); returnErr == nil && err != nil {
			returnErr = fmt.Errorf("close mds binary after hashing: %w", err)
		}
	}()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash mds binary: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func selectionArguments(request CertifyRequest) ([]string, error) {
	sources := 0
	if request.All {
		sources++
	}
	if request.Profile != "" {
		sources++
	}
	if len(request.Components) > 0 {
		sources++
	}
	if sources != 1 {
		return nil, errors.New(
			"choose exactly one of all, profile, or components for certification",
		)
	}
	switch {
	case request.All:
		return []string{"--all"}, nil
	case request.Profile != "":
		if err := validateBoundedValue("profile", request.Profile); err != nil {
			return nil, err
		}
		return []string{"--profile", request.Profile}, nil
	default:
		arguments := make([]string, 0, len(request.Components)*2)
		for _, component := range request.Components {
			if err := validateBoundedValue("component", component); err != nil {
				return nil, err
			}
			arguments = append(arguments, "--component", component)
		}
		return arguments, nil
	}
}

func runMDS(
	ctx context.Context,
	binary string,
	arguments []string,
	allowUnready bool,
) ([]byte, bool, error) {
	result, err := transport.NewLocal().Run(ctx, transport.Command{
		Executable:  binary,
		Arguments:   arguments,
		Timeout:     5 * time.Minute,
		OutputLimit: 4 << 20,
	})
	if err != nil {
		var commandError *transport.CommandError
		if !allowUnready || !errors.As(err, &commandError) ||
			commandError.Result.ExitCode != cli.ExitActionRequired ||
			strings.TrimSpace(result.Stdout) == "" {
			return nil, false, err
		}
		return []byte(result.Stdout), true, nil
	}
	return []byte(result.Stdout), false, nil
}

func validateDoctorExit(actionRequired, ready bool) error {
	if actionRequired == ready {
		return errors.New(
			"doctor exit status does not match report readiness",
		)
	}
	return nil
}

func readCLIIdentity(ctx context.Context, binary string) (CLIIdentity, error) {
	output, _, err := runMDS(ctx, binary, []string{"--version"}, false)
	if err != nil {
		return CLIIdentity{}, fmt.Errorf("read mds identity: %w", err)
	}
	line := strings.TrimSpace(string(output))
	if strings.ContainsAny(line, "\r\n") {
		return CLIIdentity{}, errors.New("mds version output must be one line")
	}
	const prefix = "mds version "
	if !strings.HasPrefix(line, prefix) {
		return CLIIdentity{}, fmt.Errorf("unexpected mds version output %q", line)
	}
	revision := strings.TrimPrefix(line, prefix)
	identity, err := parseReleaseCLIRevision(revision)
	if err != nil {
		return CLIIdentity{}, err
	}
	return identity, nil
}

func parseReleaseCLIRevision(revision string) (CLIIdentity, error) {
	match := cliRevisionPattern.FindStringSubmatch(revision)
	if match == nil {
		return CLIIdentity{}, fmt.Errorf(
			"mds version does not carry an exact release commit: %q",
			revision,
		)
	}
	date, err := time.Parse(time.RFC3339, match[3])
	if !cliVersionPattern.MatchString(match[1]) ||
		!cliCommitPattern.MatchString(match[2]) ||
		err != nil ||
		date.UTC().Format(time.RFC3339) != match[3] {
		return CLIIdentity{}, fmt.Errorf(
			"actual target certification requires a canonical production release identity: %q",
			revision,
		)
	}
	return CLIIdentity{
		Version: match[1], Commit: match[2],
		Date: match[3], Revision: revision,
	}, nil
}

func doctorSnapshot(
	report doctor.Report,
) (DoctorSnapshot, error) {
	identity, err := targetIdentity(report.Target)
	if err != nil {
		return DoctorSnapshot{}, fmt.Errorf("validate doctor target: %w", err)
	}
	checks := make([]ComponentCheck, 0, len(report.Checks))
	for _, check := range report.Checks {
		checks = append(checks, ComponentCheck{
			ActionID: check.ActionID, ComponentID: check.ComponentID,
			Status: check.Status, ReasonCode: check.ReasonCode,
			RequestedVersion: check.RequestedVersion,
			InstalledVersion: check.InstalledVersion,
			VerifiedVersion:  check.VerifiedVersion,
		})
	}
	return DoctorSnapshot{
		SchemaVersion:   report.SchemaVersion,
		CatalogRevision: report.CatalogRevision,
		Target:          identity,
		CLIRevision:     report.Target.CLIRevision,
		Ready:           report.Ready,
		Checks:          checks,
	}, nil
}
