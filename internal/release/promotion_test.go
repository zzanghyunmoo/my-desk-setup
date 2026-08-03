package release

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/doctor"
	"github.com/zzanghyunmoo/my-desk-setup/internal/evidence"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/state"
	targetpkg "github.com/zzanghyunmoo/my-desk-setup/internal/target"
)

func TestEvidenceArchiveIsDeterministicAndExact(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "bundle")
	if err := os.Mkdir(bundle, 0o700); err != nil {
		t.Fatal(err)
	}
	names := []string{
		evidence.ChecksumsFile,
		evidence.DoctorFile,
		evidence.ManifestFile,
		evidence.PlanFile,
	}
	for _, name := range names {
		if err := os.WriteFile(
			filepath.Join(bundle, name),
			[]byte("fixture-"+name+"\n"),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	first := filepath.Join(t.TempDir(), "first.zip")
	second := filepath.Join(t.TempDir(), "second.zip")
	if err := writeEvidenceArchive(first, bundle); err != nil {
		t.Fatalf("writeEvidenceArchive(first): %v", err)
	}
	if err := writeEvidenceArchive(second, bundle); err != nil {
		t.Fatalf("writeEvidenceArchive(second): %v", err)
	}
	firstBytes, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("evidence archives are not deterministic")
	}
	reader, err := zip.OpenReader(first)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if len(reader.File) != len(names) {
		t.Fatalf("archive entry count = %d", len(reader.File))
	}
	for index, entry := range reader.File {
		if entry.Name != names[index] || entry.Mode().Perm() != 0o600 {
			t.Fatalf("archive entry %d = %q mode=%o", index, entry.Name, entry.Mode())
		}
	}
}

func TestPromoteRequiresExactTargetSetAndReleaseBinaryIdentity(t *testing.T) {
	dist := buildFixtureRelease(t)
	releaseManifest, err := Verify(dist)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	targets := []targetpkg.Kind{
		targetpkg.KindMacOSHost,
		targetpkg.KindWindowsHost,
		targetpkg.KindWSLGuest,
		targetpkg.KindLimaGuest,
	}
	for _, kind := range targets {
		if err := os.Mkdir(filepath.Join(root, string(kind)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	expectedRevision := fmt.Sprintf(
		"%s (commit=%s, date=%s)",
		releaseManifest.Version,
		releaseManifest.Commit,
		releaseManifest.Date,
	)
	cohort := "cert-20260730T000000Z-" + releaseManifest.Commit[:8]
	calls := make(map[string]int)
	report, err := promoteWithVerifier(PromotionOptions{
		ReleaseDir:     dist,
		EvidenceRoot:   root,
		ExpectedCommit: releaseManifest.Commit,
		ExpectedCohort: cohort,
		Now:            time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
		MaxAge:         24 * time.Hour,
	}, func(bundle string, options evidence.VerifyOptions) (evidence.Manifest, error) {
		kind := targetpkg.Kind(filepath.Base(bundle))
		calls[string(kind)]++
		artifactOS := expectedArtifactOS(kind)
		var artifact Artifact
		for _, candidate := range releaseManifest.Artifacts {
			if candidate.OS == artifactOS {
				artifact = candidate
				break
			}
		}
		manifest := evidence.Manifest{
			Status: evidence.StatusVerified, Cohort: cohort,
			Target: evidence.TargetIdentity{
				ID: requiredTargetID(kind), Kind: kind,
			},
			CLI:             evidence.CLIIdentity{Revision: expectedRevision},
			BinarySHA256:    artifact.BinarySHA256,
			CatalogRevision: releaseManifest.CatalogRevision,
			PlanDigest:      "sha256:plan-" + string(kind),
			CapturedAtUnix:  promotionFixtureCapture(),
		}
		if calls[string(kind)] == 2 {
			if options.ExpectedCLIRevision != expectedRevision ||
				options.ExpectedCatalogRevision != releaseManifest.CatalogRevision ||
				options.ExpectedPlanDigest != manifest.PlanDigest ||
				options.ExpectedTargetID != manifest.Target.ID ||
				options.ExpectedBinarySHA256 != manifest.BinarySHA256 ||
				options.ExpectedCohort != cohort ||
				!options.RequireVerified {
				return evidence.Manifest{}, fmt.Errorf(
					"second strict verification was not fully bound: %#v",
					options,
				)
			}
		}
		return manifest, nil
	})
	if err != nil {
		t.Fatalf("Promote() error = %v", err)
	}
	if report.Commit != releaseManifest.Commit ||
		report.Cohort != cohort ||
		report.CatalogRevision != releaseManifest.CatalogRevision ||
		len(report.Targets) != 4 {
		t.Fatalf("promotion report = %#v", report)
	}
	for _, kind := range targets {
		if calls[string(kind)] != 2 {
			t.Fatalf("verification calls[%s] = %d, want 2", kind, calls[string(kind)])
		}
	}
}

func TestPromoteFailsClosedForMissingDuplicateOrMismatchedEvidence(t *testing.T) {
	dist := buildFixtureRelease(t)
	manifest, err := Verify(dist)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		dirs   []string
		mutate func(evidence.Manifest) evidence.Manifest
		want   string
	}{
		{
			name: "missing target",
			dirs: []string{"macos-host", "windows-host", "wsl-guest"},
			want: "exactly 4",
		},
		{
			name: "duplicate target kind",
			dirs: []string{"one", "two", "three", "four"},
			mutate: func(value evidence.Manifest) evidence.Manifest {
				value.Target.Kind = targetpkg.KindMacOSHost
				value.Target.ID = "macos-host:local"
				return value
			},
			want: "duplicate target kind",
		},
		{
			name: "binary mismatch",
			dirs: []string{"macos-host", "windows-host", "wsl-guest", "lima-guest"},
			mutate: func(value evidence.Manifest) evidence.Manifest {
				value.BinarySHA256 = strings.Repeat("f", 64)
				return value
			},
			want: "does not match a release artifact",
		},
		{
			name: "non-standard guest target",
			dirs: []string{"macos-host", "windows-host", "wsl-guest", "lima-guest"},
			mutate: func(value evidence.Manifest) evidence.Manifest {
				if value.Target.Kind == targetpkg.KindWSLGuest {
					value.Target.ID = "wsl-guest:personal"
				}
				return value
			},
			want: "non-standard ID",
		},
		{
			name: "blocked target",
			dirs: []string{"macos-host", "windows-host", "wsl-guest", "lima-guest"},
			mutate: func(value evidence.Manifest) evidence.Manifest {
				value.Status = evidence.StatusBlocked
				return value
			},
			want: "not verified",
		},
		{
			name: "capture outside cohort window",
			dirs: []string{"macos-host", "windows-host", "wsl-guest", "lima-guest"},
			mutate: func(value evidence.Manifest) evidence.Manifest {
				value.CapturedAtUnix = json.Number(strconv.FormatInt(
					time.Date(2026, 7, 30, 5, 0, 0, 0, time.UTC).Unix(),
					10,
				))
				return value
			},
			want: "outside the certification cohort window",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for _, name := range test.dirs {
				if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			bundleKinds := make(map[string]targetpkg.Kind)
			_, promoteErr := promoteWithVerifier(PromotionOptions{
				ReleaseDir: dist, EvidenceRoot: root,
				ExpectedCommit: manifest.Commit,
				ExpectedCohort: "cert-20260730T000000Z-" + manifest.Commit[:8],
				Now:            time.Now().UTC(), MaxAge: time.Hour,
			}, func(bundle string, _ evidence.VerifyOptions) (evidence.Manifest, error) {
				kinds := []targetpkg.Kind{
					targetpkg.KindMacOSHost,
					targetpkg.KindWindowsHost,
					targetpkg.KindWSLGuest,
					targetpkg.KindLimaGuest,
				}
				kind, exists := bundleKinds[bundle]
				if !exists {
					kind = kinds[len(bundleKinds)%len(kinds)]
					bundleKinds[bundle] = kind
				}
				osName := expectedArtifactOS(kind)
				var binarySHA string
				for _, artifact := range manifest.Artifacts {
					if artifact.OS == osName {
						binarySHA = artifact.BinarySHA256
						break
					}
				}
				value := evidence.Manifest{
					Status: evidence.StatusVerified,
					Cohort: "cert-20260730T000000Z-" + manifest.Commit[:8],
					Target: evidence.TargetIdentity{
						ID: requiredTargetID(kind), Kind: kind,
					},
					CLI: evidence.CLIIdentity{Revision: fmt.Sprintf(
						"%s (commit=%s, date=%s)",
						manifest.Version, manifest.Commit, manifest.Date,
					)},
					BinarySHA256: binarySHA, CatalogRevision: manifest.CatalogRevision,
					PlanDigest:     "sha256:plan",
					CapturedAtUnix: promotionFixtureCapture(),
				}
				if test.mutate != nil {
					value = test.mutate(value)
				}
				return value, nil
			})
			if promoteErr == nil || !strings.Contains(promoteErr.Error(), test.want) {
				t.Fatalf("Promote() error = %v, want %q", promoteErr, test.want)
			}
		})
	}
}

func TestVerifyPromotionReportRebindsStableReportToRelease(t *testing.T) {
	dist := buildFixtureRelease(t)
	manifest, err := Verify(dist)
	if err != nil {
		t.Fatal(err)
	}
	report := PromotionReport{
		SchemaVersion: PromotionSchemaVersion,
		Version:       manifest.Version, Commit: manifest.Commit,
		Cohort:          "cert-20260730T000000Z-" + manifest.Commit[:8],
		CatalogRevision: manifest.CatalogRevision,
	}
	archiveDir := filepath.Join(t.TempDir(), "evidence")
	if err := os.Mkdir(archiveDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, targetSpec := range requiredPromotionTargets {
		var artifact Artifact
		for _, candidate := range manifest.Artifacts {
			if candidate.OS == targetSpec.OS {
				artifact = candidate
				break
			}
		}
		evidenceArchive := fmt.Sprintf(
			"mds_%s_certification_%s_%s.zip",
			report.Version,
			targetSpec.Kind,
			report.Cohort,
		)
		promoted := PromotedTarget{
			ID: targetSpec.ID, Kind: targetSpec.Kind,
			Status:          evidence.StatusVerified,
			BinarySHA256:    artifact.BinarySHA256,
			ReleaseArtifact: artifact.Name,
			EvidenceArtifact: fmt.Sprintf(
				"target-evidence-%s-%s-%s-123-1",
				targetSpec.Kind,
				report.Commit,
				report.Cohort,
			),
			EvidenceArchive: evidenceArchive,
			CapturedAtUnix: time.Date(
				2026, 7, 30, 0, 0, 0, 0, time.UTC,
			).Unix(),
		}
		writeVerifiedEvidenceArchiveFixture(
			t,
			filepath.Join(archiveDir, evidenceArchive),
			manifest,
			report.Cohort,
			&promoted,
		)
		report.Targets = append(report.Targets, promoted)
	}
	path := filepath.Join(t.TempDir(), "release-promotion.json")
	if err := WritePromotionReport(path, report); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPromotionReport(
		dist,
		path,
		archiveDir,
		manifest.Commit,
		report.Cohort,
	); err != nil {
		t.Fatalf("VerifyPromotionReport() error = %v", err)
	}
	versionOne := report
	versionOne.SchemaVersion = "mds.release-promotion/v1"
	versionOnePath := filepath.Join(t.TempDir(), "release-promotion.json")
	if err := WritePromotionReport(versionOnePath, versionOne); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPromotionReport(
		dist,
		versionOnePath,
		archiveDir,
		manifest.Commit,
		report.Cohort,
	); err == nil || !strings.Contains(err.Error(), "exact release identity") {
		t.Fatalf("VerifyPromotionReport(v1) error = %v", err)
	}
	timestampMismatch := report
	timestampMismatch.Targets = append(
		[]PromotedTarget(nil),
		report.Targets...,
	)
	timestampMismatch.Targets[0].CapturedAtUnix++
	timestampMismatchPath := filepath.Join(
		t.TempDir(),
		"release-promotion.json",
	)
	if err := WritePromotionReport(
		timestampMismatchPath,
		timestampMismatch,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPromotionReport(
		dist,
		timestampMismatchPath,
		archiveDir,
		manifest.Commit,
		report.Cohort,
	); err == nil || !strings.Contains(err.Error(), "capture timestamp") {
		t.Fatalf("VerifyPromotionReport(timestamp mismatch) error = %v", err)
	}
	report.Commit = strings.Repeat("f", len(manifest.Commit))
	tampered := filepath.Join(t.TempDir(), "release-promotion.json")
	if err := WritePromotionReport(tampered, report); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPromotionReport(
		dist,
		tampered,
		archiveDir,
		manifest.Commit,
		report.Cohort,
	); err == nil ||
		!strings.Contains(err.Error(), "exact release identity") {
		t.Fatalf("VerifyPromotionReport(tampered) error = %v", err)
	}

	report.Commit = manifest.Commit
	report.Targets[0].Status = evidence.StatusBlocked
	blocked := filepath.Join(t.TempDir(), "release-promotion.json")
	if err := WritePromotionReport(blocked, report); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPromotionReport(
		dist,
		blocked,
		archiveDir,
		manifest.Commit,
		report.Cohort,
	); err == nil || !strings.Contains(err.Error(), "not verified") {
		t.Fatalf("VerifyPromotionReport(blocked) error = %v", err)
	}
}

func writeVerifiedEvidenceArchiveFixture(
	t *testing.T,
	archivePath string,
	releaseManifest Manifest,
	cohort string,
	promoted *PromotedTarget,
) {
	t.Helper()
	id, err := targetpkg.ParseID(promoted.ID)
	if err != nil {
		t.Fatal(err)
	}
	revision := fmt.Sprintf(
		"%s (commit=%s, date=%s)",
		releaseManifest.Version,
		releaseManifest.Commit,
		releaseManifest.Date,
	)
	facts := targetpkg.Facts{
		ID: id, OS: expectedArtifactOS(promoted.Kind),
		OSVersion: "fixture", Architecture: "amd64",
		Reachable:       true,
		CLIRevision:     revision,
		CatalogRevision: releaseManifest.CatalogRevision,
	}
	switch promoted.Kind {
	case targetpkg.KindWSLGuest, targetpkg.KindLimaGuest:
		facts.RuntimeVersion = "fixture"
		facts.ImageRevision = "sha256:" + strings.Repeat("b", 64)
		facts.ImageProvenance = "https://example.invalid/ubuntu-26.04.img"
		commitment, commitmentErr := targetpkg.GuestCreationNonceCommitment(
			strings.Repeat("c", 64),
		)
		if commitmentErr != nil {
			t.Fatal(commitmentErr)
		}
		facts.ImageCreationNonceCommitment = commitment
	}
	action := planning.Action{
		ID: promoted.ID + "/go", ComponentID: "go",
		TargetID: promoted.ID, Status: planning.ActionPlanned,
		Version: "1.0.0",
	}
	plan := planning.Plan{
		SchemaVersion:   planning.PlanSchema,
		CatalogRevision: releaseManifest.CatalogRevision,
		Target:          facts, Selection: []string{"go"},
		Actions: []planning.Action{action},
	}
	plan.Digest, err = planning.Digest(plan)
	if err != nil {
		t.Fatal(err)
	}
	promoted.PlanDigest = plan.Digest
	fingerprint, err := facts.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	targetIdentity := evidence.TargetIdentity{
		ID: promoted.ID, Kind: promoted.Kind, Fingerprint: fingerprint,
	}
	check := evidence.ComponentCheck{
		ActionID: action.ID, ComponentID: action.ComponentID,
		Status: "ready", ReasonCode: "ready",
		RequestedVersion: action.Version,
		InstalledVersion: action.Version,
		VerifiedVersion:  action.Version,
	}
	receipt := state.Receipt{
		SchemaVersion: state.ReceiptSchema,
		PlanDigest:    plan.Digest, CatalogRevision: plan.CatalogRevision,
		TargetID: promoted.ID, Complete: true,
		Outcomes: []state.ActionOutcome{{
			ActionID: action.ID, Status: "ready",
			RequestedVersion: action.Version,
			InstalledVersion: action.Version,
			VerifiedVersion:  action.Version,
		}},
	}
	repeat := receipt
	repeat.Outcomes = append([]state.ActionOutcome(nil), receipt.Outcomes...)
	repeat.Outcomes[0].Noop = true
	manifest := evidence.Manifest{
		SchemaVersion: evidence.SchemaVersion,
		CaptureKind:   evidence.CaptureKindActualTarget,
		Status:        evidence.StatusVerified, Cohort: cohort,
		CapturedAtUnix: json.Number(strconv.FormatInt(
			promoted.CapturedAtUnix,
			10,
		)),
		Target: targetIdentity,
		CLI: evidence.CLIIdentity{
			Version:  releaseManifest.Version,
			Commit:   releaseManifest.Commit,
			Date:     releaseManifest.Date,
			Revision: revision,
		},
		BinarySHA256:    promoted.BinarySHA256,
		CatalogRevision: releaseManifest.CatalogRevision,
		PlanDigest:      plan.Digest,
		Components:      []evidence.ComponentCheck{check},
		ApplyReceipt:    &receipt, RepeatReceipt: &repeat,
	}
	doctor := evidence.DoctorSnapshot{
		SchemaVersion:   doctor.SchemaVersion,
		CatalogRevision: releaseManifest.CatalogRevision,
		Target:          targetIdentity, CLIRevision: revision,
		Ready: true, Checks: []evidence.ComponentCheck{check},
	}
	bundle := t.TempDir()
	writeEvidenceJSONFixture(t, filepath.Join(bundle, evidence.PlanFile), plan)
	writeEvidenceJSONFixture(t, filepath.Join(bundle, evidence.DoctorFile), doctor)
	writeEvidenceJSONFixture(t, filepath.Join(bundle, evidence.ManifestFile), manifest)
	writeEvidenceChecksumsFixture(t, bundle)
	if err := writeEvidenceArchive(archivePath, bundle); err != nil {
		t.Fatal(err)
	}
	promoted.EvidenceSHA256, _, err = fileIdentity(archivePath)
	if err != nil {
		t.Fatal(err)
	}
}

func writeEvidenceJSONFixture(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeEvidenceChecksumsFixture(t *testing.T, root string) {
	t.Helper()
	names := []string{
		evidence.DoctorFile,
		evidence.ManifestFile,
		evidence.PlanFile,
	}
	sort.Strings(names)
	var lines []string
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		lines = append(lines, hex.EncodeToString(sum[:])+"  "+name)
	}
	if err := os.WriteFile(
		filepath.Join(root, evidence.ChecksumsFile),
		[]byte(strings.Join(lines, "\n")+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
}

func promotionFixtureCapture() json.Number {
	return json.Number(strconv.FormatInt(
		time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC).Unix(),
		10,
	))
}
