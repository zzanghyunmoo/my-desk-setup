package release

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	exactartifact "github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
	"github.com/zzanghyunmoo/my-desk-setup/internal/durable"
	"github.com/zzanghyunmoo/my-desk-setup/internal/evidence"
	targetpkg "github.com/zzanghyunmoo/my-desk-setup/internal/target"
)

const (
	PromotionSchemaVersion        = "mds.release-promotion/v1"
	maximumCohortCaptureWindow    = 4 * time.Hour
	certificationCaptureClockSkew = 5 * time.Minute
)

type promotionTargetSpec struct {
	Kind targetpkg.Kind
	ID   string
	OS   string
}

var requiredPromotionTargets = []promotionTargetSpec{
	{Kind: targetpkg.KindMacOSHost, ID: "macos-host:local", OS: "darwin"},
	{Kind: targetpkg.KindWindowsHost, ID: "windows-host:local", OS: "windows"},
	{Kind: targetpkg.KindWSLGuest, ID: "wsl-guest:Ubuntu-26.04", OS: "linux"},
	{Kind: targetpkg.KindLimaGuest, ID: "lima-guest:mds", OS: "linux"},
}

type PromotionOptions struct {
	ReleaseDir     string
	EvidenceRoot   string
	ExpectedCommit string
	ExpectedCohort string
	Now            time.Time
	MaxAge         time.Duration
}

type PromotedTarget struct {
	ID              string          `json:"id"`
	Kind            targetpkg.Kind  `json:"kind"`
	Status          evidence.Status `json:"status"`
	PlanDigest      string          `json:"plan_digest"`
	BinarySHA256    string          `json:"binary_sha256"`
	ReleaseArtifact string          `json:"release_artifact"`
	CapturedAtUnix  int64           `json:"captured_at_unix"`
}

type PromotionReport struct {
	SchemaVersion   string           `json:"schema_version"`
	Version         string           `json:"version"`
	Commit          string           `json:"commit"`
	Cohort          string           `json:"cohort"`
	CatalogRevision string           `json:"catalog_revision"`
	Targets         []PromotedTarget `json:"targets"`
}

type evidenceVerifier func(
	string,
	evidence.VerifyOptions,
) (evidence.Manifest, error)

func Promote(options PromotionOptions) (PromotionReport, error) {
	return promoteWithVerifier(options, evidence.Verify)
}

func promoteWithVerifier(
	options PromotionOptions,
	verify evidenceVerifier,
) (PromotionReport, error) {
	if options.ReleaseDir == "" || options.EvidenceRoot == "" {
		return PromotionReport{}, errors.New(
			"release directory and evidence root are required",
		)
	}
	if !commitPattern.MatchString(options.ExpectedCommit) {
		return PromotionReport{}, errors.New(
			"expected release commit must be a full lowercase commit SHA",
		)
	}
	cohortCommitPrefix, err := evidence.CertificationCohortCommitPrefix(
		options.ExpectedCohort,
	)
	if err != nil {
		return PromotionReport{}, err
	}
	if cohortCommitPrefix != options.ExpectedCommit[:8] {
		return PromotionReport{}, errors.New(
			"certification cohort does not match the release commit",
		)
	}
	cohortTimestamp, err := evidence.CertificationCohortTimestamp(
		options.ExpectedCohort,
	)
	if err != nil {
		return PromotionReport{}, err
	}
	if options.Now.IsZero() || options.MaxAge <= 0 {
		return PromotionReport{}, errors.New(
			"promotion requires a current timestamp and bounded positive evidence age",
		)
	}
	if cohortTimestamp.After(options.Now.Add(certificationCaptureClockSkew)) {
		return PromotionReport{}, errors.New(
			"certification cohort timestamp is in the future",
		)
	}
	releaseManifest, err := Verify(options.ReleaseDir)
	if err != nil {
		return PromotionReport{}, fmt.Errorf("verify release: %w", err)
	}
	if releaseManifest.Commit != options.ExpectedCommit {
		return PromotionReport{}, fmt.Errorf(
			"release commit mismatch: manifest=%s expected=%s",
			releaseManifest.Commit,
			options.ExpectedCommit,
		)
	}
	bundles, err := exactEvidenceDirectories(options.EvidenceRoot)
	if err != nil {
		return PromotionReport{}, err
	}
	expectedCLIRevision := fmt.Sprintf(
		"%s (commit=%s, date=%s)",
		releaseManifest.Version,
		releaseManifest.Commit,
		releaseManifest.Date,
	)
	artifactsByBinary := make(map[string][]Artifact)
	for _, artifact := range releaseManifest.Artifacts {
		artifactsByBinary[artifact.BinarySHA256] = append(
			artifactsByBinary[artifact.BinarySHA256],
			artifact,
		)
	}
	byKind := make(map[targetpkg.Kind]PromotedTarget, len(bundles))
	var earliestCapture time.Time
	var latestCapture time.Time
	for _, bundle := range bundles {
		initial, verifyErr := verify(bundle, evidence.VerifyOptions{
			ExpectedCLIRevision:     expectedCLIRevision,
			ExpectedCatalogRevision: releaseManifest.CatalogRevision,
			ExpectedCohort:          options.ExpectedCohort,
			Now:                     options.Now,
			MaxAge:                  options.MaxAge,
		})
		if verifyErr != nil {
			return PromotionReport{}, fmt.Errorf(
				"verify target evidence %s: %w",
				filepath.Base(bundle),
				verifyErr,
			)
		}
		if initial.Status != evidence.StatusVerified {
			return PromotionReport{}, fmt.Errorf(
				"target evidence %q is %s, not verified",
				initial.Target.ID,
				initial.Status,
			)
		}
		capturedAtUnix, parseErr := strconv.ParseInt(
			string(initial.CapturedAtUnix),
			10,
			64,
		)
		if parseErr != nil || capturedAtUnix < 0 {
			return PromotionReport{}, fmt.Errorf(
				"target evidence %q has invalid capture timestamp",
				initial.Target.ID,
			)
		}
		capturedAt := time.Unix(capturedAtUnix, 0).UTC()
		if capturedAt.Before(
			cohortTimestamp.Add(-certificationCaptureClockSkew),
		) || capturedAt.After(cohortTimestamp.Add(maximumCohortCaptureWindow)) {
			return PromotionReport{}, fmt.Errorf(
				"target evidence %q falls outside the certification cohort window",
				initial.Target.ID,
			)
		}
		if earliestCapture.IsZero() || capturedAt.Before(earliestCapture) {
			earliestCapture = capturedAt
		}
		if latestCapture.IsZero() || capturedAt.After(latestCapture) {
			latestCapture = capturedAt
		}
		kind := initial.Target.Kind
		targetSpec, required := promotionTarget(kind)
		if !required {
			return PromotionReport{}, fmt.Errorf(
				"unexpected target kind %q",
				kind,
			)
		}
		if _, exists := byKind[kind]; exists {
			return PromotionReport{}, fmt.Errorf(
				"duplicate target kind %q",
				kind,
			)
		}
		if initial.Target.ID != targetSpec.ID {
			return PromotionReport{}, fmt.Errorf(
				"target kind %q has non-standard ID %q; want %q",
				kind,
				initial.Target.ID,
				targetSpec.ID,
			)
		}
		artifacts := artifactsByBinary[initial.BinarySHA256]
		if len(artifacts) == 0 {
			return PromotionReport{}, fmt.Errorf(
				"target %q binary %s does not match a release artifact",
				initial.Target.ID,
				initial.BinarySHA256,
			)
		}
		if len(artifacts) != 1 {
			return PromotionReport{}, fmt.Errorf(
				"target %q binary identity is ambiguous across release artifacts",
				initial.Target.ID,
			)
		}
		artifact := artifacts[0]
		if artifact.OS != targetSpec.OS {
			return PromotionReport{}, fmt.Errorf(
				"target kind %q cannot certify %s artifact %q",
				kind,
				artifact.OS,
				artifact.Name,
			)
		}
		strict, verifyErr := verify(bundle, evidence.VerifyOptions{
			ExpectedCLIRevision:     expectedCLIRevision,
			ExpectedCatalogRevision: releaseManifest.CatalogRevision,
			ExpectedPlanDigest:      initial.PlanDigest,
			ExpectedTargetID:        initial.Target.ID,
			ExpectedBinarySHA256:    artifact.BinarySHA256,
			ExpectedCohort:          options.ExpectedCohort,
			RequireVerified:         true,
			Now:                     options.Now,
			MaxAge:                  options.MaxAge,
		})
		if verifyErr != nil {
			return PromotionReport{}, fmt.Errorf(
				"strictly verify target evidence %s: %w",
				initial.Target.ID,
				verifyErr,
			)
		}
		if !reflect.DeepEqual(strict, initial) {
			return PromotionReport{}, fmt.Errorf(
				"target evidence %q changed between verification passes",
				initial.Target.ID,
			)
		}
		byKind[kind] = PromotedTarget{
			ID: initial.Target.ID, Kind: kind, Status: initial.Status,
			PlanDigest:      initial.PlanDigest,
			BinarySHA256:    initial.BinarySHA256,
			ReleaseArtifact: artifact.Name,
			CapturedAtUnix:  capturedAtUnix,
		}
	}
	if latestCapture.Sub(earliestCapture) > maximumCohortCaptureWindow {
		return PromotionReport{}, errors.New(
			"target evidence capture spread exceeds the certification cohort window",
		)
	}
	report := PromotionReport{
		SchemaVersion:   PromotionSchemaVersion,
		Version:         releaseManifest.Version,
		Commit:          releaseManifest.Commit,
		Cohort:          options.ExpectedCohort,
		CatalogRevision: releaseManifest.CatalogRevision,
		Targets:         make([]PromotedTarget, 0, len(requiredPromotionTargets)),
	}
	for _, targetSpec := range requiredPromotionTargets {
		promoted, exists := byKind[targetSpec.Kind]
		if !exists {
			return PromotionReport{}, fmt.Errorf(
				"missing required target evidence for %q",
				targetSpec.Kind,
			)
		}
		report.Targets = append(report.Targets, promoted)
	}
	return report, nil
}

func exactEvidenceDirectories(root string) ([]string, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect evidence root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("evidence root must be a real directory")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read evidence root: %w", err)
	}
	if len(entries) != len(requiredPromotionTargets) {
		return nil, fmt.Errorf(
			"evidence root must contain exactly 4 target bundles, got %d",
			len(entries),
		)
	}
	bundles := make([]string, 0, len(entries))
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		entryInfo, statErr := os.Lstat(path)
		if statErr != nil {
			return nil, fmt.Errorf("inspect evidence bundle %q: %w", entry.Name(), statErr)
		}
		if !entryInfo.IsDir() || entryInfo.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf(
				"evidence bundle %q must be a real directory",
				entry.Name(),
			)
		}
		bundles = append(bundles, path)
	}
	sort.Strings(bundles)
	return bundles, nil
}

func expectedArtifactOS(kind targetpkg.Kind) string {
	targetSpec, exists := promotionTarget(kind)
	if !exists {
		return ""
	}
	return targetSpec.OS
}

func requiredTargetID(kind targetpkg.Kind) string {
	targetSpec, exists := promotionTarget(kind)
	if !exists {
		return ""
	}
	return targetSpec.ID
}

func promotionTarget(kind targetpkg.Kind) (promotionTargetSpec, bool) {
	for _, targetSpec := range requiredPromotionTargets {
		if kind == targetSpec.Kind {
			return targetSpec, true
		}
	}
	return promotionTargetSpec{}, false
}

func WritePromotionReport(path string, report PromotionReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode promotion report: %w", err)
	}
	data = append(data, '\n')
	if err := durable.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write promotion report: %w", err)
	}
	return nil
}

func VerifyPromotionReport(
	releaseDir,
	reportPath,
	expectedCommit,
	expectedCohort string,
) (PromotionReport, error) {
	releaseManifest, err := Verify(releaseDir)
	if err != nil {
		return PromotionReport{}, fmt.Errorf("verify release: %w", err)
	}
	if releaseManifest.Commit != expectedCommit {
		return PromotionReport{}, fmt.Errorf(
			"release commit mismatch: manifest=%s expected=%s",
			releaseManifest.Commit,
			expectedCommit,
		)
	}
	prefix, err := evidence.CertificationCohortCommitPrefix(expectedCohort)
	if err != nil {
		return PromotionReport{}, err
	}
	if prefix != expectedCommit[:8] {
		return PromotionReport{}, errors.New(
			"certification cohort does not match the release commit",
		)
	}
	cohortTimestamp, err := evidence.CertificationCohortTimestamp(expectedCohort)
	if err != nil {
		return PromotionReport{}, err
	}
	info, err := os.Lstat(reportPath)
	if err != nil {
		return PromotionReport{}, fmt.Errorf("inspect promotion report: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return PromotionReport{}, errors.New(
			"promotion report must be a regular non-symlink file",
		)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		return PromotionReport{}, fmt.Errorf("read promotion report: %w", err)
	}
	var report PromotionReport
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return PromotionReport{}, fmt.Errorf("decode promotion report: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return PromotionReport{}, errors.New(
				"decode promotion report: multiple JSON values",
			)
		}
		return PromotionReport{}, fmt.Errorf(
			"decode promotion report trailing data: %w",
			err,
		)
	}
	if report.SchemaVersion != PromotionSchemaVersion ||
		report.Version != releaseManifest.Version ||
		report.Commit != releaseManifest.Commit ||
		report.Cohort != expectedCohort ||
		report.CatalogRevision != releaseManifest.CatalogRevision {
		return PromotionReport{}, errors.New(
			"promotion report does not match the exact release identity",
		)
	}
	if len(report.Targets) != len(requiredPromotionTargets) {
		return PromotionReport{}, fmt.Errorf(
			"promotion report has %d targets, want %d",
			len(report.Targets),
			len(requiredPromotionTargets),
		)
	}
	artifacts := make(map[string]Artifact, len(releaseManifest.Artifacts))
	for _, artifact := range releaseManifest.Artifacts {
		artifacts[artifact.Name] = artifact
	}
	var earliestCapture time.Time
	var latestCapture time.Time
	for index, targetSpec := range requiredPromotionTargets {
		promoted := report.Targets[index]
		if promoted.Kind != targetSpec.Kind ||
			promoted.ID != targetSpec.ID {
			return PromotionReport{}, fmt.Errorf(
				"promotion target %d does not match required target %q",
				index,
				targetSpec.ID,
			)
		}
		if promoted.Status != evidence.StatusVerified {
			return PromotionReport{}, fmt.Errorf(
				"promotion target %q is not verified: %q",
				promoted.ID,
				promoted.Status,
			)
		}
		capturedAt := time.Unix(promoted.CapturedAtUnix, 0).UTC()
		if promoted.CapturedAtUnix < 0 ||
			capturedAt.Before(
				cohortTimestamp.Add(-certificationCaptureClockSkew),
			) ||
			capturedAt.After(cohortTimestamp.Add(maximumCohortCaptureWindow)) {
			return PromotionReport{}, fmt.Errorf(
				"promotion target %q has an invalid capture timestamp",
				promoted.ID,
			)
		}
		if earliestCapture.IsZero() || capturedAt.Before(earliestCapture) {
			earliestCapture = capturedAt
		}
		if latestCapture.IsZero() || capturedAt.After(latestCapture) {
			latestCapture = capturedAt
		}
		if exactartifact.ValidateSHA256(promoted.BinarySHA256) != nil {
			return PromotionReport{}, fmt.Errorf(
				"promotion target %q has invalid binary identity",
				promoted.ID,
			)
		}
		if !strings.HasPrefix(promoted.PlanDigest, "sha256:") ||
			exactartifact.ValidateSHA256(strings.TrimPrefix(
				promoted.PlanDigest,
				"sha256:",
			)) != nil {
			return PromotionReport{}, fmt.Errorf(
				"promotion target %q has invalid plan digest",
				promoted.ID,
			)
		}
		artifact, exists := artifacts[promoted.ReleaseArtifact]
		if !exists ||
			artifact.BinarySHA256 != promoted.BinarySHA256 ||
			artifact.OS != targetSpec.OS {
			return PromotionReport{}, fmt.Errorf(
				"promotion target %q does not match its release artifact",
				promoted.ID,
			)
		}
	}
	if latestCapture.Sub(earliestCapture) > maximumCohortCaptureWindow {
		return PromotionReport{}, errors.New(
			"promotion report capture spread exceeds the certification cohort window",
		)
	}
	return report, nil
}
