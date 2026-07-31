package release

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/evidence"
	targetpkg "github.com/zzanghyunmoo/my-desk-setup/internal/target"
)

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
					PlanDigest: "sha256:plan",
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
	for _, kind := range requiredPromotionTargets {
		osName := expectedArtifactOS(kind)
		var artifact Artifact
		for _, candidate := range manifest.Artifacts {
			if candidate.OS == osName {
				artifact = candidate
				break
			}
		}
		report.Targets = append(report.Targets, PromotedTarget{
			ID: requiredTargetID(kind), Kind: kind,
			Status:          evidence.StatusVerified,
			PlanDigest:      "sha256:" + strings.Repeat("a", 64),
			BinarySHA256:    artifact.BinarySHA256,
			ReleaseArtifact: artifact.Name,
		})
	}
	path := filepath.Join(t.TempDir(), "release-promotion.json")
	if err := WritePromotionReport(path, report); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPromotionReport(
		dist,
		path,
		manifest.Commit,
		report.Cohort,
	); err != nil {
		t.Fatalf("VerifyPromotionReport() error = %v", err)
	}
	report.Commit = strings.Repeat("f", len(manifest.Commit))
	tampered := filepath.Join(t.TempDir(), "release-promotion.json")
	if err := WritePromotionReport(tampered, report); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPromotionReport(
		dist,
		tampered,
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
		manifest.Commit,
		report.Cohort,
	); err == nil || !strings.Contains(err.Error(), "not verified") {
		t.Fatalf("VerifyPromotionReport(blocked) error = %v", err)
	}
}
