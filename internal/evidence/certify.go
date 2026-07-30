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
	"runtime"
	"strconv"
	"strings"
	"time"

	catalogdata "github.com/zzanghyunmoo/my-desk-setup/catalog"
	exactartifact "github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/cli"
	"github.com/zzanghyunmoo/my-desk-setup/internal/doctor"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/state"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

const (
	certificationReadTimeout   = 10 * time.Minute
	certificationApplyTimeout  = 2 * time.Hour
	certificationRepeatTimeout = 30 * time.Minute
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
	certifiedTarget, environment, err := certificationTarget(request)
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
		environment,
		false,
		certificationReadTimeout,
	)
	if err != nil {
		return Manifest{}, fmt.Errorf("capture read-only plan: %w", err)
	}
	var plan planning.Plan
	if err := decodeStrict(planOutput, &plan); err != nil {
		return Manifest{}, fmt.Errorf("decode plan output: %w", err)
	}
	applyArguments := append(
		[]string{"apply", "--plan-digest", plan.Digest},
		common...,
	)
	applyOutput, applyActionRequired, err := runMDS(
		ctx,
		request.MDSPath,
		applyArguments,
		environment,
		true,
		certificationApplyTimeout,
	)
	if err != nil {
		return Manifest{}, fmt.Errorf("execute reviewed apply: %w", err)
	}
	var applyReceipt state.Receipt
	if err := decodeStrict(applyOutput, &applyReceipt); err != nil {
		return Manifest{}, fmt.Errorf("decode apply receipt: %w", err)
	}
	if err := validateCertificationReceipt(
		applyReceipt,
		plan,
		applyActionRequired,
		false,
	); err != nil {
		return Manifest{}, fmt.Errorf("validate apply receipt: %w", err)
	}

	var repeatReceipt *state.Receipt
	if applyReceipt.Complete {
		repeatOutput, repeatActionRequired, err := runMDS(
			ctx,
			request.MDSPath,
			applyArguments,
			environment,
			true,
			certificationRepeatTimeout,
		)
		if err != nil {
			return Manifest{}, fmt.Errorf("execute repeat apply: %w", err)
		}
		var repeated state.Receipt
		if err := decodeStrict(repeatOutput, &repeated); err != nil {
			return Manifest{}, fmt.Errorf("decode repeat apply receipt: %w", err)
		}
		if err := validateCertificationReceipt(
			repeated,
			plan,
			repeatActionRequired,
			true,
		); err != nil {
			return Manifest{}, fmt.Errorf("validate repeat apply receipt: %w", err)
		}
		repeatReceipt = &repeated
	}

	doctorOutput, doctorActionRequired, err := runMDS(
		ctx,
		request.MDSPath,
		append([]string{"doctor"}, common...),
		environment,
		true,
		certificationReadTimeout,
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
	if plan.Target.ID != certifiedTarget.ID ||
		plan.Target.Architecture != certifiedTarget.Architecture ||
		plan.Target.ImageRevision != certifiedTarget.ImageRevision ||
		plan.Target.ImageProvenance != certifiedTarget.ImageProvenance ||
		plan.Target.ImageCreationNonce != certifiedTarget.ImageCreationNonce {
		return Manifest{}, errors.New(
			"captured plan target does not match independently certified runtime identity",
		)
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
		!applyReceipt.Complete ||
		repeatReceipt == nil ||
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
		Components: components, ApplyReceipt: &applyReceipt,
		RepeatReceipt: repeatReceipt,
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
		exactartifact.ValidateSHA256(request.ExpectedBinarySHA256) != nil {
		return errors.New("expected mds binary SHA-256 must be 64 lowercase hex characters")
	}
	id, err := target.ParseID(request.TargetID)
	if err != nil {
		return fmt.Errorf("invalid certification target: %w", err)
	}
	switch id.Kind {
	case target.KindWSLGuest, target.KindLimaGuest:
		if exactartifact.ValidateSHA256(
			request.ExpectedGuestCreationNonce,
		) != nil {
			return errors.New(
				"guest certification requires the 64-character lowercase creation nonce from the host ownership record",
			)
		}
	default:
		if request.ExpectedGuestCreationNonce != "" {
			return errors.New(
				"host certification must not provide a guest creation nonce",
			)
		}
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

func certificationTarget(
	request CertifyRequest,
) (target.Facts, map[string]string, error) {
	id, err := target.ParseID(request.TargetID)
	if err != nil {
		return target.Facts{}, nil, err
	}
	var facts target.Facts
	if request.RuntimeProbe != nil {
		facts, err = request.RuntimeProbe(id)
	} else {
		facts, err = probeCertificationTarget(
			id,
			request.ExpectedGuestCreationNonce,
		)
	}
	if err != nil {
		return target.Facts{}, nil, err
	}
	if facts.ID != id {
		return target.Facts{}, nil, errors.New(
			"certification runtime probe returned a different target",
		)
	}
	environment := make(map[string]string, 4)
	switch id.Kind {
	case target.KindWSLGuest:
		environment["WSL_DISTRO_NAME"] = id.Name
	case target.KindLimaGuest:
		environment["LIMA_INSTANCE"] = id.Name
	}
	if facts.ImageRevision != "" {
		environment["MDS_IMAGE_REVISION"] = facts.ImageRevision
	}
	if facts.ImageProvenance != "" {
		environment["MDS_IMAGE_PROVENANCE"] = facts.ImageProvenance
	}
	if facts.ImageCreationNonce != "" {
		environment["MDS_IMAGE_CREATION_NONCE"] = facts.ImageCreationNonce
	}
	return facts, environment, nil
}

func probeCertificationTarget(
	id target.ID,
	expectedGuestCreationNonce string,
) (target.Facts, error) {
	switch id.Kind {
	case target.KindMacOSHost:
		if runtime.GOOS != "darwin" || id.Name != "local" {
			return target.Facts{}, errors.New(
				"macOS certification requires the local Darwin runtime",
			)
		}
		return target.Facts{
			ID: id, OS: "darwin", Architecture: runtime.GOARCH, Reachable: true,
		}, nil
	case target.KindWindowsHost:
		if runtime.GOOS != "windows" || id.Name != "local" {
			return target.Facts{}, errors.New(
				"windows certification requires the local Windows runtime",
			)
		}
		return target.Facts{
			ID: id, OS: "windows", Architecture: runtime.GOARCH, Reachable: true,
		}, nil
	case target.KindWSLGuest:
		if runtime.GOOS != "linux" ||
			strings.TrimSpace(os.Getenv("WSL_DISTRO_NAME")) != id.Name {
			return target.Facts{}, errors.New(
				"wsl certification requires the exact requested WSL distribution",
			)
		}
		kernel, err := os.ReadFile("/proc/sys/kernel/osrelease")
		if err != nil || !strings.Contains(
			strings.ToLower(string(kernel)),
			"microsoft",
		) {
			return target.Facts{}, errors.New(
				"wsl certification requires an independently observed Microsoft kernel",
			)
		}
	case target.KindLimaGuest:
		if runtime.GOOS != "linux" ||
			strings.TrimSpace(os.Getenv("LIMA_INSTANCE")) != id.Name {
			return target.Facts{}, errors.New(
				"lima certification requires the exact requested Lima instance",
			)
		}
		marker, err := os.Lstat("/run/lima-ssh-ready")
		if err != nil || !marker.Mode().IsRegular() ||
			marker.Mode()&os.ModeSymlink != 0 ||
			!runtimeMarkerOwnedByRoot(marker) {
			return target.Facts{}, errors.New(
				"lima certification requires the root-owned Lima runtime marker",
			)
		}
	default:
		return target.Facts{}, fmt.Errorf(
			"unsupported certification target kind %q",
			id.Kind,
		)
	}
	markerContent, err := readRootOwnedRuntimeMarker(target.ImageIdentityPath)
	if err != nil {
		return target.Facts{}, fmt.Errorf(
			"certification requires the provisioned guest image identity marker: %w",
			err,
		)
	}
	observedImage, err := target.ParseImageIdentity(markerContent)
	if err != nil {
		return target.Facts{}, fmt.Errorf(
			"parse provisioned guest image identity marker: %w",
			err,
		)
	}
	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		return target.Facts{}, fmt.Errorf("load certification catalog: %w", err)
	}
	specification, exists := environment.Targets["ubuntu-26.04"]
	if !exists {
		return target.Facts{}, errors.New(
			"certification catalog is missing the Ubuntu target",
		)
	}
	images := specification.Images
	if id.Kind == target.KindWSLGuest {
		images = specification.WSLImages
	}
	image, exists := images[runtime.GOARCH]
	if !exists {
		return target.Facts{}, fmt.Errorf(
			"certification catalog has no %s image for %s",
			id.Kind,
			runtime.GOARCH,
		)
	}
	if err := validateCertifiedGuestImage(
		observedImage,
		image,
		expectedGuestCreationNonce,
	); err != nil {
		return target.Facts{}, err
	}
	return target.Facts{
		ID: id, OS: "linux", Architecture: runtime.GOARCH, Reachable: true,
		ImageRevision:      observedImage.Revision,
		ImageProvenance:    observedImage.Provenance,
		ImageCreationNonce: observedImage.CreationNonce,
	}, nil
}

func validateCertifiedGuestImage(
	observedImage target.ImageIdentity,
	image catalog.ImageSpec,
	expectedGuestCreationNonce string,
) error {
	expectedRevision := "sha256:" + image.SHA256
	if observedImage.Revision != expectedRevision ||
		observedImage.Provenance != image.URL {
		return fmt.Errorf(
			"provisioned guest image identity does not match the certification catalog: observed=%s %s expected=%s %s",
			observedImage.Revision,
			observedImage.Provenance,
			expectedRevision,
			image.URL,
		)
	}
	if observedImage.CreationNonce != expectedGuestCreationNonce {
		return errors.New(
			"provisioned guest creation identity does not match the protected host ownership record",
		)
	}
	return nil
}

func runMDS(
	ctx context.Context,
	binary string,
	arguments []string,
	environment map[string]string,
	allowUnready bool,
	timeout time.Duration,
) ([]byte, bool, error) {
	result, err := transport.NewLocal().Run(ctx, transport.Command{
		Executable:  binary,
		Arguments:   arguments,
		Environment: environment,
		Timeout:     timeout,
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

func validateCertificationReceipt(
	receipt state.Receipt,
	plan planning.Plan,
	actionRequired,
	requireNoop bool,
) error {
	if receipt.SchemaVersion != state.ReceiptSchema ||
		receipt.PlanDigest != plan.Digest ||
		receipt.CatalogRevision != plan.CatalogRevision ||
		receipt.TargetID != plan.Target.ID.String() {
		return errors.New("receipt identity does not match the reviewed plan")
	}
	if actionRequired == receipt.Complete {
		return errors.New("apply exit status does not match receipt completion")
	}
	if len(receipt.Outcomes) != len(plan.Actions) {
		return errors.New("receipt does not cover every reviewed action")
	}
	allReady := true
	for index, action := range plan.Actions {
		outcome := receipt.Outcomes[index]
		if outcome.ActionID != action.ID ||
			outcome.RequestedVersion != action.Version {
			return fmt.Errorf(
				"receipt outcome %d does not match reviewed action %s",
				index,
				action.ID,
			)
		}
		switch outcome.Status {
		case "ready", "action-required", "unsupported", "blocked", "failed":
		default:
			return fmt.Errorf(
				"receipt outcome %s has unknown status %q",
				action.ID,
				outcome.Status,
			)
		}
		if action.Status == planning.ActionActionRequired &&
			outcome.Status != string(planning.ActionActionRequired) {
			return fmt.Errorf(
				"receipt outcome %s does not preserve action-required status",
				action.ID,
			)
		}
		if action.Status == planning.ActionUnsupported &&
			outcome.Status != string(planning.ActionUnsupported) {
			return fmt.Errorf(
				"receipt outcome %s does not preserve unsupported status",
				action.ID,
			)
		}
		if outcome.Noop && outcome.Status != "ready" {
			return fmt.Errorf(
				"receipt outcome %s claims no-op without readiness",
				action.ID,
			)
		}
		if outcome.Status != "ready" {
			allReady = false
		}
	}
	if receipt.Complete != allReady {
		return errors.New(
			"receipt completion does not match reviewed action outcomes",
		)
	}
	if requireNoop {
		if !receipt.Complete {
			return errors.New("repeat apply receipt is incomplete")
		}
		for _, outcome := range receipt.Outcomes {
			if !outcome.Noop {
				return fmt.Errorf(
					"repeat apply mutated or reverified action %s instead of converging as a no-op",
					outcome.ActionID,
				)
			}
		}
	}
	return nil
}

func readCLIIdentity(ctx context.Context, binary string) (CLIIdentity, error) {
	output, _, err := runMDS(
		ctx,
		binary,
		[]string{"--version"},
		nil,
		false,
		certificationReadTimeout,
	)
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
