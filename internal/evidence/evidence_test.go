package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/capability"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/cli"
	"github.com/zzanghyunmoo/my-desk-setup/internal/doctor"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/state"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
)

const (
	fixtureCommit          = "0123456789abcdef0123456789abcdef01234567"
	fixtureCLIRevision     = "0.1.0 (commit=" + fixtureCommit + ", date=2026-07-29T00:00:00Z)"
	fixtureCatalogRevision = "sha256:catalog"
	fixtureCohort          = "cert-20260730T000000Z-01234567"
)

func TestCertifyAndVerifyReadyActualTarget(t *testing.T) {
	bundle := certifyFixture(t, true)

	manifest, err := Verify(bundle, VerifyOptions{
		ExpectedCLIRevision:     fixtureCLIRevision,
		ExpectedCatalogRevision: fixtureCatalogRevision,
		ExpectedCohort:          fixtureCohort,
	})
	if err != nil {
		t.Fatalf("Verify(): %v", err)
	}
	if manifest.Status != StatusVerified {
		t.Fatalf("status = %q, want %q", manifest.Status, StatusVerified)
	}
	if manifest.CaptureKind != CaptureKindActualTarget {
		t.Fatalf("capture_kind = %q, want actual target", manifest.CaptureKind)
	}
	if manifest.Cohort != fixtureCohort {
		t.Fatalf("cohort = %q, want %q", manifest.Cohort, fixtureCohort)
	}
	if manifest.CLI.Commit != fixtureCommit || manifest.CLI.Version != "0.1.0" {
		t.Fatalf("CLI identity = %+v", manifest.CLI)
	}
}

func TestCertifyPublishesCommitmentWithoutRawGuestNonce(t *testing.T) {
	bundle := certifyFixture(t, true)
	rawNonce := []byte(strings.Repeat("a", 64))
	for _, name := range expectedFiles {
		content, err := os.ReadFile(filepath.Join(bundle, name))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		if strings.Contains(string(content), string(rawNonce)) {
			t.Fatalf("%s contains raw guest creation nonce", name)
		}
		if strings.Contains(string(content), `"image_creation_nonce"`) ||
			strings.Contains(string(content), `"creation_nonce"`) {
			t.Fatalf("%s contains a raw nonce field", name)
		}
	}
	planContent, err := os.ReadFile(filepath.Join(bundle, PlanFile))
	if err != nil {
		t.Fatalf("ReadFile(plan): %v", err)
	}
	if !strings.Contains(
		string(planContent),
		`"image_creation_nonce_commitment": "sha256:`,
	) {
		t.Fatalf("plan does not contain nonce commitment: %s", planContent)
	}
}

func TestVerifyRejectsLegacyRawGuestNonceField(t *testing.T) {
	bundle := certifyFixture(t, true)
	path := filepath.Join(bundle, PlanFile)
	var document map[string]any
	readJSON(t, path, &document)
	targetDocument, ok := document["target"].(map[string]any)
	if !ok {
		t.Fatalf("plan target = %#v", document["target"])
	}
	targetDocument["image_creation_nonce"] = strings.Repeat("a", 64)
	writeJSON(t, path, document)
	writeChecksums(t, bundle)

	_, err := Verify(bundle, VerifyOptions{})
	assertErrorContains(t, err, "raw guest creation nonce")
}

func TestCertifyAcceptsActionRequiredDoctorReport(t *testing.T) {
	bundle := certifyFixture(t, false)

	manifest, err := Verify(bundle, VerifyOptions{
		ExpectedCLIRevision:     fixtureCLIRevision,
		ExpectedCatalogRevision: fixtureCatalogRevision,
	})
	if err != nil {
		t.Fatalf("Verify(): %v", err)
	}
	if manifest.Status != StatusBlocked {
		t.Fatalf("status = %q, want %q", manifest.Status, StatusBlocked)
	}
}

func TestRequireVerifiedAcceptsOnlyVerifiedEvidence(t *testing.T) {
	for _, test := range []struct {
		name      string
		ready     bool
		wantError string
	}{
		{name: "verified", ready: true},
		{name: "blocked", ready: false, wantError: "not verified"},
	} {
		t.Run(test.name, func(t *testing.T) {
			bundle := certifyFixture(t, test.ready)
			var manifest Manifest
			readJSON(t, filepath.Join(bundle, ManifestFile), &manifest)

			_, err := Verify(bundle, VerifyOptions{
				ExpectedCLIRevision:     manifest.CLI.Revision,
				ExpectedCatalogRevision: manifest.CatalogRevision,
				ExpectedPlanDigest:      manifest.PlanDigest,
				ExpectedTargetID:        manifest.Target.ID,
				ExpectedBinarySHA256:    manifest.BinarySHA256,
				ExpectedCohort:          manifest.Cohort,
				RequireVerified:         true,
			})
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("Verify(RequireVerified): %v", err)
				}
				return
			}
			assertErrorContains(t, err, test.wantError)
		})
	}
}

func TestDoctorExitMustMatchReportReadiness(t *testing.T) {
	tests := []struct {
		name           string
		actionRequired bool
		ready          bool
		wantError      bool
	}{
		{name: "ready success", ready: true},
		{
			name:           "blocked action required",
			actionRequired: true,
			ready:          false,
		},
		{
			name:           "ready action required mismatch",
			actionRequired: true,
			ready:          true,
			wantError:      true,
		},
		{
			name:      "blocked success mismatch",
			ready:     false,
			wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateDoctorExit(test.actionRequired, test.ready)
			if (err != nil) != test.wantError {
				t.Fatalf(
					"validateDoctorExit(%t, %t) error=%v, wantError=%t",
					test.actionRequired,
					test.ready,
					err,
					test.wantError,
				)
			}
		})
	}
}

func TestCertifyBlocksFunctionalVerificationFailure(t *testing.T) {
	adapter := &evidenceDoctorAdapter{
		verifyError: errors.New("runtime functional probe failed"),
	}
	bundle := certifyFixtureWithReport(
		t,
		true,
		func(plan planning.Plan, _ doctor.Report) doctor.Report {
			report, err := doctor.Run(context.Background(), plan, adapter)
			if err != nil {
				t.Fatalf("doctor.Run(): %v", err)
			}
			return report
		},
	)

	manifest, err := Verify(bundle, VerifyOptions{
		ExpectedCLIRevision:     fixtureCLIRevision,
		ExpectedCatalogRevision: fixtureCatalogRevision,
	})
	if err != nil {
		t.Fatalf("Verify(): %v", err)
	}
	if manifest.Status != StatusBlocked {
		t.Fatalf("status = %q, want %q", manifest.Status, StatusBlocked)
	}
	var snapshot DoctorSnapshot
	readJSON(t, filepath.Join(bundle, DoctorFile), &snapshot)
	if snapshot.Ready || len(snapshot.Checks) != 1 {
		t.Fatalf("doctor snapshot = %+v, want one unready check", snapshot)
	}
	check := snapshot.Checks[0]
	if check.Status != "unready" ||
		check.ReasonCode != "functional-verification-failed" ||
		check.VerifiedVersion != "" {
		t.Fatalf("doctor check = %+v, want unverified functional failure", check)
	}
	if adapter.applyCalls != 0 || adapter.verifyCalls != 1 {
		t.Fatalf(
			"doctor adapter calls: apply=%d verify=%d, want apply=0 verify=1",
			adapter.applyCalls,
			adapter.verifyCalls,
		)
	}
}

func TestCertifyRequiresCapabilitiesForSelectedIDE(t *testing.T) {
	readyReceipt := readyCapabilityReceipt([]string{"nvim-jvm"})

	for _, test := range []struct {
		name    string
		receipt *capability.Receipt
		want    Status
	}{
		{name: "missing", want: StatusBlocked},
		{name: "ready", receipt: &readyReceipt, want: StatusVerified},
	} {
		t.Run(test.name, func(t *testing.T) {
			bundle := certifyFixtureWithTransforms(
				t,
				true,
				func(plan *planning.Plan) {
					plan.Selection = []string{"nvim-jvm"}
					plan.Actions[0].ComponentID = "nvim-jvm"
				},
				func(_ planning.Plan, report doctor.Report) doctor.Report {
					report.Checks[0].ComponentID = "nvim-jvm"
					report.Capabilities = test.receipt
					return report
				},
			)
			manifest, err := Verify(bundle, VerifyOptions{})
			if err != nil {
				t.Fatalf("Verify(): %v", err)
			}
			if manifest.Status != test.want {
				t.Fatalf("status = %q, want %q", manifest.Status, test.want)
			}
		})
	}
}

func TestVerifyRejectsCapabilityReceiptTampering(t *testing.T) {
	readyReceipt := readyCapabilityReceipt([]string{"nvim-jvm"})
	bundle := certifyFixtureWithTransforms(
		t,
		true,
		func(plan *planning.Plan) {
			plan.Selection = []string{"nvim-jvm"}
			plan.Actions[0].ComponentID = "nvim-jvm"
		},
		func(_ planning.Plan, report doctor.Report) doctor.Report {
			report.Checks[0].ComponentID = "nvim-jvm"
			report.Capabilities = &readyReceipt
			return report
		},
	)
	rewriteManifest(t, bundle, func(manifest *Manifest) {
		manifest.Capabilities.Checks[0].Status = capability.StatusFailed
	})

	_, err := Verify(bundle, VerifyOptions{})
	assertErrorContains(t, err, "capability outcomes")
}

func TestParseCLIIdentityRequiresCanonicalProductionRelease(t *testing.T) {
	tests := []struct {
		name     string
		revision string
	}{
		{
			name:     "short commit",
			revision: "0.1.0 (commit=abc123, date=2026-07-29T00:00:00Z)",
		},
		{
			name:     "invalid version",
			revision: "latest (commit=" + fixtureCommit + ", date=2026-07-29T00:00:00Z)",
		},
		{
			name:     "noncanonical date",
			revision: "0.1.0 (commit=" + fixtureCommit + ", date=2026-07-29T09:00:00+09:00)",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			match := cliRevisionPattern.FindStringSubmatch(test.revision)
			if match == nil {
				t.Fatalf("test revision %q does not exercise identity validation", test.revision)
			}
			_, err := parseCLIIdentity(CLIIdentity{
				Version: match[1], Commit: match[2],
				Date: match[3], Revision: test.revision,
			})
			assertErrorContains(t, err, "production")
		})
	}
}

func readyCapabilityReceipt(components []string) capability.Receipt {
	specifications := capability.Expected(components)
	checks := make([]capability.CapabilityCheck, 0, len(specifications))
	for _, specification := range specifications {
		if specification.Kind == capability.KindDAP {
			checks = append(checks, capability.NewDAPCheck(
				specification.ID,
				specification.ComponentID,
				capability.StatusPass,
				"ready",
				capability.DAPOutcome{
					BreakpointVerified: true, StoppedAtSource: true,
					StoppedSourceID: "fixture", StoppedLine: 1,
					StackObserved: true, ScopesObserved: true,
					KnownVariablePresent: true, Continued: true,
					SteppedIn: true, SteppedOver: true, Terminated: true,
				},
			))
			continue
		}
		checks = append(checks, capability.NewCheck(
			specification.ID, specification.Kind, specification.ComponentID,
			capability.StatusPass, "ready", "",
		))
	}
	return capability.Aggregate(capability.ExpectedIDs(components), checks)
}

func TestVerifyRejectsBlockedEvidencePromotedToVerified(t *testing.T) {
	bundle := certifyFixture(t, false)
	rewriteManifest(t, bundle, func(manifest *Manifest) {
		manifest.Status = StatusVerified
	})

	_, err := Verify(bundle, VerifyOptions{})
	assertErrorContains(t, err, "status")
}

func TestVerifyRejectsImplementedStatusAsActualTargetEvidence(t *testing.T) {
	bundle := certifyFixture(t, true)
	rewriteManifest(t, bundle, func(manifest *Manifest) {
		manifest.Status = StatusImplemented
	})

	_, err := Verify(bundle, VerifyOptions{})
	assertErrorContains(t, err, "cannot claim status")
}

func TestVerifyRejectsTargetEvidenceVersionOne(t *testing.T) {
	bundle := certifyFixture(t, true)
	rewriteManifest(t, bundle, func(manifest *Manifest) {
		manifest.SchemaVersion = "mds.target-evidence/v1"
	})
	_, err := Verify(bundle, VerifyOptions{})
	assertErrorContains(t, err, "evidence schema")
}

func TestVerifyRejectsDoctorSchemaAndCLIRevisionTampering(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*DoctorSnapshot)
		want   string
	}{
		{
			name: "schema",
			mutate: func(snapshot *DoctorSnapshot) {
				snapshot.SchemaVersion = "mds.doctor/v1"
			},
			want: "doctor schema",
		},
		{
			name: "CLI revision",
			mutate: func(snapshot *DoctorSnapshot) {
				snapshot.CLIRevision = "0.1.0 (commit=forged, date=2026-07-29T00:00:00Z)"
			},
			want: "CLI revision",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			bundle := certifyFixture(t, true)
			rewriteDoctor(t, bundle, test.mutate)

			_, err := Verify(bundle, VerifyOptions{})
			assertErrorContains(t, err, test.want)
		})
	}
}

func TestVerifyRejectsPlanDigestAndStaleRevisions(t *testing.T) {
	t.Run("checksum mismatch", func(t *testing.T) {
		bundle := certifyFixture(t, true)
		path := filepath.Join(bundle, DoctorFile)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(): %v", err)
		}
		if err := os.WriteFile(path, append(content, '\n'), 0o600); err != nil {
			t.Fatalf("WriteFile(): %v", err)
		}

		_, err = Verify(bundle, VerifyOptions{})
		assertErrorContains(t, err, "checksums")
	})

	t.Run("manifest plan digest mismatch", func(t *testing.T) {
		bundle := certifyFixture(t, true)
		rewriteManifest(t, bundle, func(manifest *Manifest) {
			manifest.PlanDigest = "sha256:forged"
		})

		_, err := Verify(bundle, VerifyOptions{})
		assertErrorContains(t, err, "plan digest")
	})

	for _, test := range []struct {
		name    string
		options VerifyOptions
		want    string
	}{
		{
			name: "stale CLI",
			options: VerifyOptions{
				ExpectedCLIRevision: "0.2.0 (commit=new, date=2026-07-30T00:00:00Z)",
			},
			want: "stale CLI",
		},
		{
			name: "stale catalog",
			options: VerifyOptions{
				ExpectedCatalogRevision: "sha256:new-catalog",
			},
			want: "stale catalog",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			bundle := certifyFixture(t, true)
			_, err := Verify(bundle, test.options)
			assertErrorContains(t, err, test.want)
		})
	}
}

func TestVerifyRejectsCredentialPathAndAuthMaterial(t *testing.T) {
	for _, test := range []struct {
		name     string
		material string
		want     string
	}{
		{
			name:     "credential",
			material: `"api_token":"must-not-publish"`,
			want:     "credential",
		},
		{
			name:     "personal Unix home",
			material: `"detail":"/Users/alice/private/config"`,
			want:     "personal absolute home path",
		},
		{
			name:     "personal Windows home",
			material: `"detail":"C:\\Users\\alice\\private\\config"`,
			want:     "personal absolute home path",
		},
		{
			name:     "auth command",
			material: `"verification":["gh","auth","status"]`,
			want:     "authentication command",
		},
		{
			name:     "credential flag",
			material: `"verification":["tool","--token","value"]`,
			want:     "credential flag",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			bundle := certifyFixture(t, true)
			path := filepath.Join(bundle, DoctorFile)
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile(): %v", err)
			}
			content = append(content, []byte("\n"+test.material+"\n")...)
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatalf("WriteFile(): %v", err)
			}
			writeChecksums(t, bundle)

			_, err = Verify(bundle, VerifyOptions{})
			assertErrorContains(t, err, test.want)
		})
	}
}

func TestVerifyRejectsUnexpectedFileAndSymlink(t *testing.T) {
	t.Run("extra file", func(t *testing.T) {
		bundle := certifyFixture(t, true)
		if err := os.WriteFile(
			filepath.Join(bundle, "notes.txt"),
			[]byte("not part of the evidence contract"),
			0o600,
		); err != nil {
			t.Fatalf("WriteFile(): %v", err)
		}

		_, err := Verify(bundle, VerifyOptions{})
		assertErrorContains(t, err, "unexpected evidence file set")
	})

	t.Run("symlink", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("creating a symlink requires additional Windows privileges")
		}
		bundle := certifyFixture(t, true)
		planPath := filepath.Join(bundle, PlanFile)
		if err := os.Remove(planPath); err != nil {
			t.Fatalf("Remove(): %v", err)
		}
		if err := os.Symlink(DoctorFile, planPath); err != nil {
			t.Fatalf("Symlink(): %v", err)
		}
		writeChecksums(t, bundle)

		_, err := Verify(bundle, VerifyOptions{})
		assertErrorContains(t, err, "symlink")
	})
}

func TestVerifyParsesUnixTimestampAsSigned64BitDecimal(t *testing.T) {
	bundle := certifyFixture(t, true)
	path := filepath.Join(bundle, ManifestFile)
	var document map[string]any
	readJSON(t, path, &document)
	document["captured_at_unix"] = json.Number("1e3")
	writeJSON(t, path, document)
	writeChecksums(t, bundle)

	_, err := Verify(bundle, VerifyOptions{})
	assertErrorContains(t, err, "captured_at_unix")
}

func TestVerifyBindsReceiptOutcomesToReviewedActions(t *testing.T) {
	bundle := certifyFixture(t, true)
	rewriteManifest(t, bundle, func(manifest *Manifest) {
		manifest.ApplyReceipt.Outcomes[0].ActionID = "lima-guest:mds/other"
	})

	_, err := Verify(bundle, VerifyOptions{})
	assertErrorContains(t, err, "does not match reviewed action")
}

func TestVerifyRejectsUnknownReceiptOutcomeStatus(t *testing.T) {
	bundle := certifyFixture(t, true)
	rewriteManifest(t, bundle, func(manifest *Manifest) {
		manifest.ApplyReceipt.Outcomes[0].Status = "verified-ish"
		manifest.ApplyReceipt.Complete = false
		manifest.RepeatReceipt = nil
		manifest.Status = StatusBlocked
	})

	_, err := Verify(bundle, VerifyOptions{})
	assertErrorContains(t, err, "unknown status")
}

func TestCertificationTargetPropagatesIndependentlyProbedGuestIdentity(t *testing.T) {
	id, err := target.NewID(target.KindLimaGuest, "mds")
	if err != nil {
		t.Fatalf("NewID(): %v", err)
	}
	facts := target.Facts{
		ID:                           id,
		OS:                           "linux",
		Architecture:                 "arm64",
		ImageRevision:                "sha256:image",
		ImageProvenance:              "https://example.invalid/ubuntu.img",
		ImageCreationNonceCommitment: fixtureNonceCommitment(t, strings.Repeat("a", 64)),
	}
	certified, environment, err := certificationTarget(
		func(probed target.ID) (target.Facts, error) {
			if probed != id {
				return target.Facts{}, errors.New("unexpected target")
			}
			return facts, nil
		},
		id,
		facts.ImageCreationNonceCommitment,
	)
	if err != nil {
		t.Fatalf("certificationTarget(): %v", err)
	}
	if certified != facts {
		t.Fatalf("certified target = %+v, want %+v", certified, facts)
	}
	if environment["LIMA_INSTANCE"] != "mds" ||
		environment["MDS_IMAGE_REVISION"] != "sha256:image" ||
		environment["MDS_IMAGE_PROVENANCE"] != facts.ImageProvenance ||
		environment["MDS_IMAGE_CREATION_NONCE_COMMITMENT"] !=
			facts.ImageCreationNonceCommitment {
		t.Fatalf("certification environment = %#v", environment)
	}
}

func TestCertifiedGuestImageRejectsReplacementNonce(t *testing.T) {
	image := catalog.ImageSpec{
		URL:    "https://example.invalid/ubuntu.img",
		SHA256: strings.Repeat("a", 64),
	}
	err := validateCertifiedGuestImage(
		target.ImageIdentity{
			Revision:   "sha256:" + image.SHA256,
			Provenance: image.URL,
			CreationNonceCommitment: fixtureNonceCommitment(
				t,
				strings.Repeat("b", 64),
			),
		},
		image,
		fixtureNonceCommitment(t, strings.Repeat("c", 64)),
	)
	assertErrorContains(t, err, "creation identity")
}

func TestTargetIdentityCompletenessKeepsPartialTargetsBlocked(t *testing.T) {
	id, err := target.NewID(target.KindLimaGuest, "mds")
	if err != nil {
		t.Fatalf("NewID(): %v", err)
	}
	facts := target.Facts{
		ID: id, OS: "linux", OSVersion: "26.04", Architecture: "arm64",
		RuntimeVersion: "1.0.0", ImageRevision: "sha256:image",
		ImageProvenance: "https://example.invalid/ubuntu.img",
		ImageCreationNonceCommitment: fixtureNonceCommitment(
			t,
			strings.Repeat("a", 64),
		),
		Reachable: true, CLIRevision: fixtureCLIRevision,
		CatalogRevision: fixtureCatalogRevision,
	}
	if !targetIdentityComplete(facts) {
		t.Fatal("complete Lima identity reported incomplete")
	}
	facts.ImageRevision = ""
	if targetIdentityComplete(facts) {
		t.Fatal("Lima identity without reviewed image revision reported complete")
	}
}

func TestCertifyRejectsRepeatApplyMutationAfterDistinctFirstApply(t *testing.T) {
	bundle := certifyFixture(t, true)
	root := filepath.Dir(bundle)
	repeatReceiptPath := filepath.Join(root, "fixture-repeat-receipt.json")
	var repeated state.Receipt
	readJSON(t, repeatReceiptPath, &repeated)
	repeated.Outcomes[0].Noop = false
	writeJSON(t, repeatReceiptPath, repeated)
	if err := os.Remove(filepath.Join(root, "fixture-apply-count")); err != nil {
		t.Fatalf("reset fake apply count: %v", err)
	}
	var plan planning.Plan
	readJSON(t, filepath.Join(root, "fixture-plan.json"), &plan)
	binaryPath := filepath.Join(root, "fake-mds")

	_, err := Certify(context.Background(), CertifyRequest{
		MDSPath:              binaryPath,
		TargetID:             plan.Target.ID.String(),
		OutputDir:            filepath.Join(root, "mutating-repeat-bundle"),
		Cohort:               fixtureCohort,
		ExpectedBinarySHA256: fileSHA256Fixture(t, binaryPath),
		ExpectedPlanDigest:   plan.Digest,
		Components:           []string{"go"},
		ExpectedGuestCreationNonceCommitment: plan.Target.
			ImageCreationNonceCommitment,
		Now: func() time.Time { return time.Unix(1<<40, 0).UTC() },
		RuntimeProbe: func(id target.ID) (target.Facts, error) {
			if id != plan.Target.ID {
				return target.Facts{}, errors.New("unexpected target")
			}
			return plan.Target, nil
		},
	})
	assertErrorContains(t, err, "repeat apply mutated")
	if _, statErr := os.Stat(filepath.Join(root, "mutating-repeat-bundle")); !errors.Is(
		statErr,
		os.ErrNotExist,
	) {
		t.Fatalf("failed certification published output: %v", statErr)
	}
}

func TestCertifyRejectsReviewedPlanMismatchBeforeApply(t *testing.T) {
	bundle := certifyFixture(t, true)
	root := filepath.Dir(bundle)
	applyCountPath := filepath.Join(root, "fixture-apply-count")
	if err := os.Remove(applyCountPath); err != nil {
		t.Fatalf("reset fake apply count: %v", err)
	}
	var plan planning.Plan
	readJSON(t, filepath.Join(root, "fixture-plan.json"), &plan)
	binaryPath := filepath.Join(root, "fake-mds")

	_, err := Certify(context.Background(), CertifyRequest{
		MDSPath:              binaryPath,
		TargetID:             plan.Target.ID.String(),
		OutputDir:            filepath.Join(root, "plan-mismatch-bundle"),
		Cohort:               fixtureCohort,
		ExpectedBinarySHA256: fileSHA256Fixture(t, binaryPath),
		Components:           []string{"go"},
		ExpectedPlanDigest: "sha256:" +
			strings.Repeat("f", 64),
		ExpectedGuestCreationNonceCommitment: plan.Target.
			ImageCreationNonceCommitment,
		RuntimeProbe: func(id target.ID) (target.Facts, error) {
			if id != plan.Target.ID {
				return target.Facts{}, errors.New("unexpected target")
			}
			return plan.Target, nil
		},
	})
	assertErrorContains(t, err, "plan digest mismatch before apply")
	if _, statErr := os.Lstat(applyCountPath); !errors.Is(
		statErr,
		os.ErrNotExist,
	) {
		t.Fatalf("plan mismatch executed apply: %v", statErr)
	}
}

func certifyFixture(t *testing.T, ready bool) string {
	return certifyFixtureWithReport(
		t,
		ready,
		func(_ planning.Plan, report doctor.Report) doctor.Report {
			return report
		},
	)
}

func certifyFixtureWithReport(
	t *testing.T,
	ready bool,
	transform func(planning.Plan, doctor.Report) doctor.Report,
) string {
	return certifyFixtureWithTransforms(t, ready, nil, transform)
}

func certifyFixtureWithTransforms(
	t *testing.T,
	ready bool,
	transformPlan func(*planning.Plan),
	transform func(planning.Plan, doctor.Report) doctor.Report,
) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the focused fake binary fixture uses a POSIX executable")
	}

	root := t.TempDir()
	targetID, err := target.NewID(target.KindLimaGuest, "mds")
	if err != nil {
		t.Fatalf("NewID(): %v", err)
	}
	facts := target.Facts{
		ID: targetID, OS: "linux", OSVersion: "26.04", Architecture: "arm64",
		RuntimeVersion: "1.0.0", ImageRevision: "sha256:image",
		ImageProvenance: "https://example.invalid/ubuntu.img",
		ImageCreationNonceCommitment: fixtureNonceCommitment(
			t,
			strings.Repeat("a", 64),
		),
		SystemdSupported: true,
		SystemdActive:    true, Reachable: true,
		CLIRevision: fixtureCLIRevision, CatalogRevision: fixtureCatalogRevision,
	}
	plan := planning.Plan{
		SchemaVersion:   planning.PlanSchema,
		CatalogRevision: fixtureCatalogRevision,
		Target:          facts,
		Selection:       []string{"go"},
		Actions: []planning.Action{
			{
				ID: "lima-guest:mds/go", ComponentID: "go",
				TargetID: targetID.String(), Status: planning.ActionPlanned,
				Version: "1.25.0", Dependencies: []string{},
				Verification: [][]string{{"go", "version"}},
			},
		},
		Blockers: []planning.Blocker{},
	}
	if transformPlan != nil {
		transformPlan(&plan)
	}
	plan.Digest, err = planning.Digest(plan)
	if err != nil {
		t.Fatalf("Digest(): %v", err)
	}
	report := doctor.Report{
		SchemaVersion: doctor.SchemaVersion, CatalogRevision: fixtureCatalogRevision,
		Target: facts, Ready: ready,
		Checks: []doctor.Check{
			{
				ActionID: "lima-guest:mds/go", ComponentID: "go",
				Status: "ready", ReasonCode: "ready",
				RequestedVersion: "1.25.0",
				InstalledVersion: "1.25.0", VerifiedVersion: "1.25.0",
			},
		},
	}
	if !ready {
		report.Checks[0].Status = "unready"
		report.Checks[0].ReasonCode = "not-installed"
		report.Checks[0].InstalledVersion = ""
		report.Checks[0].VerifiedVersion = ""
	}
	report = transform(plan, report)

	planPath := filepath.Join(root, "fixture-plan.json")
	doctorPath := filepath.Join(root, "fixture-doctor.json")
	firstReceiptPath := filepath.Join(root, "fixture-first-receipt.json")
	repeatReceiptPath := filepath.Join(root, "fixture-repeat-receipt.json")
	applyCountPath := filepath.Join(root, "fixture-apply-count")
	writeJSON(t, planPath, plan)
	writeJSON(t, doctorPath, report)
	firstReceipt := state.Receipt{
		SchemaVersion: state.ReceiptSchema,
		PlanDigest:    plan.Digest, CatalogRevision: plan.CatalogRevision,
		TargetID: plan.Target.ID.String(), Complete: true,
		Outcomes: []state.ActionOutcome{{
			ActionID: "lima-guest:mds/go", Status: "ready",
			RequestedVersion: "1.25.0", InstalledVersion: "1.25.0",
			VerifiedVersion: "1.25.0", Noop: false,
		}},
	}
	repeatReceipt := firstReceipt
	repeatReceipt.Outcomes = append([]state.ActionOutcome(nil), firstReceipt.Outcomes...)
	repeatReceipt.Outcomes[0].Noop = true
	writeJSON(t, firstReceiptPath, firstReceipt)
	writeJSON(t, repeatReceiptPath, repeatReceipt)
	binaryPath := filepath.Join(root, "fake-mds")
	doctorExit := 0
	if !report.Ready {
		doctorExit = cli.ExitActionRequired
	}
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
  --version)
    printf 'mds version %s\n'
    ;;
  plan)
    exec /bin/cat %q
    ;;
  apply)
    if [ -e %q ]; then
      exec /bin/cat %q
    fi
    : > %q
    exec /bin/cat %q
    ;;
  doctor)
    /bin/cat %q
    exit %d
    ;;
  *)
    printf 'unexpected command: %%s\n' "$1" >&2
    exit 2
    ;;
esac
`,
		fixtureCLIRevision,
		planPath,
		applyCountPath,
		repeatReceiptPath,
		applyCountPath,
		firstReceiptPath,
		doctorPath,
		doctorExit,
	)
	if err := os.WriteFile(binaryPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake mds: %v", err)
	}

	bundle := filepath.Join(root, "bundle")
	manifest, err := Certify(context.Background(), CertifyRequest{
		MDSPath: binaryPath, TargetID: targetID.String(), OutputDir: bundle,
		Cohort:               fixtureCohort,
		ExpectedBinarySHA256: fileSHA256Fixture(t, binaryPath),
		ExpectedPlanDigest:   plan.Digest,
		Components:           []string{"go"},
		ExpectedGuestCreationNonceCommitment: facts.
			ImageCreationNonceCommitment,
		Now: func() time.Time { return time.Unix(1<<40, 0).UTC() },
		RuntimeProbe: func(id target.ID) (target.Facts, error) {
			if id != plan.Target.ID {
				return target.Facts{}, errors.New("unexpected target")
			}
			return plan.Target, nil
		},
	})
	if err != nil {
		t.Fatalf("Certify(): %v", err)
	}
	expectedStatus := StatusVerified
	if !report.Ready || !capabilitiesReady(plan, report.Capabilities) {
		expectedStatus = StatusBlocked
	}
	if manifest.Status != expectedStatus {
		t.Fatalf("Certify() status = %q, want %q", manifest.Status, expectedStatus)
	}
	return bundle
}

func fixtureNonceCommitment(t *testing.T, nonce string) string {
	t.Helper()
	commitment, err := target.GuestCreationNonceCommitment(nonce)
	if err != nil {
		t.Fatalf("GuestCreationNonceCommitment(): %v", err)
	}
	return commitment
}

type evidenceDoctorAdapter struct {
	applyCalls  int
	verifyCalls int
	verifyError error
}

func (adapter *evidenceDoctorAdapter) Observe(
	_ context.Context,
	action planning.Action,
) (adapters.Observation, error) {
	return adapters.Observation{
		State: adapters.StateReady, InstalledVersion: action.Version,
	}, nil
}

func (adapter *evidenceDoctorAdapter) Apply(
	context.Context,
	planning.Action,
) error {
	adapter.applyCalls++
	return nil
}

func (adapter *evidenceDoctorAdapter) Verify(
	context.Context,
	planning.Action,
) error {
	adapter.verifyCalls++
	return adapter.verifyError
}

func rewriteManifest(t *testing.T, root string, mutate func(*Manifest)) {
	t.Helper()
	path := filepath.Join(root, ManifestFile)
	var manifest Manifest
	readJSON(t, path, &manifest)
	mutate(&manifest)
	writeJSON(t, path, manifest)
	writeChecksums(t, root)
}

func rewriteDoctor(t *testing.T, root string, mutate func(*DoctorSnapshot)) {
	t.Helper()
	path := filepath.Join(root, DoctorFile)
	var snapshot DoctorSnapshot
	readJSON(t, path, &snapshot)
	mutate(&snapshot)
	writeJSON(t, path, snapshot)
	writeChecksums(t, root)
}

func readJSON(t *testing.T, path string, value any) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.UseNumber()
	if err := decoder.Decode(value); err != nil {
		t.Fatalf("Decode(%s): %v", path, err)
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent(): %v", err)
	}
	content = append(content, '\n')
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func writeChecksums(t *testing.T, root string) {
	t.Helper()
	names := []string{DoctorFile, ManifestFile, PlanFile}
	sort.Strings(names)
	var lines []string
	for _, name := range names {
		content, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		sum := sha256.Sum256(content)
		lines = append(lines, hex.EncodeToString(sum[:])+"  "+name)
	}
	if err := os.WriteFile(
		filepath.Join(root, ChecksumsFile),
		[]byte(strings.Join(lines, "\n")+"\n"),
		0o600,
	); err != nil {
		t.Fatalf("write checksums: %v", err)
	}
}

func fileSHA256Fixture(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(want)) {
		t.Fatalf("error = %v, want substring %q", err, want)
	}
}
