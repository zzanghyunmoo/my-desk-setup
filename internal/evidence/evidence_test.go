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
	"github.com/zzanghyunmoo/my-desk-setup/internal/cli"
	"github.com/zzanghyunmoo/my-desk-setup/internal/doctor"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
)

const (
	fixtureCommit          = "0123456789abcdef0123456789abcdef01234567"
	fixtureCLIRevision     = "0.1.0 (commit=" + fixtureCommit + ", date=2026-07-29T00:00:00Z)"
	fixtureCatalogRevision = "sha256:catalog"
)

func TestCertifyAndVerifyReadyActualTarget(t *testing.T) {
	bundle := certifyFixture(t, true)

	manifest, err := Verify(bundle, VerifyOptions{
		ExpectedCLIRevision:     fixtureCLIRevision,
		ExpectedCatalogRevision: fixtureCatalogRevision,
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
	if manifest.CLI.Commit != fixtureCommit || manifest.CLI.Version != "0.1.0" {
		t.Fatalf("CLI identity = %+v", manifest.CLI)
	}
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

func TestRunMDSPreservesActionRequiredSignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the focused fake binary fixture uses a POSIX executable")
	}
	binaryPath := filepath.Join(t.TempDir(), "fake-mds")
	if err := os.WriteFile(
		binaryPath,
		[]byte("#!/bin/sh\nprintf '{}\\n'\nexit 4\n"),
		0o700,
	); err != nil {
		t.Fatalf("write fake mds: %v", err)
	}

	output, actionRequired, err := runMDS(
		context.Background(),
		binaryPath,
		[]string{"doctor"},
		true,
	)
	if err != nil {
		t.Fatalf("runMDS(): %v", err)
	}
	if strings.TrimSpace(string(output)) != "{}" || !actionRequired {
		t.Fatalf(
			"runMDS() output=%q actionRequired=%t, want JSON and true",
			output,
			actionRequired,
		)
	}
}

func TestRunMDSRejectsNonContractPartialResults(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the focused fake binary fixture uses a POSIX executable")
	}
	tests := []struct {
		name         string
		script       string
		allowUnready bool
	}{
		{
			name:         "legacy generic failure",
			script:       "#!/bin/sh\nprintf '{}\\n'\nexit 1\n",
			allowUnready: true,
		},
		{
			name:         "action required without report",
			script:       "#!/bin/sh\nexit 4\n",
			allowUnready: true,
		},
		{
			name:         "action required not allowed",
			script:       "#!/bin/sh\nprintf '{}\\n'\nexit 4\n",
			allowUnready: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binaryPath := filepath.Join(t.TempDir(), "fake-mds")
			if err := os.WriteFile(
				binaryPath,
				[]byte(test.script),
				0o700,
			); err != nil {
				t.Fatalf("write fake mds: %v", err)
			}
			if _, _, err := runMDS(
				context.Background(),
				binaryPath,
				[]string{"doctor"},
				test.allowUnready,
			); err == nil {
				t.Fatal("runMDS() error = nil, want contract rejection")
			}
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

func TestVerifyRejectsDoctorSchemaAndCLIRevisionTampering(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*DoctorSnapshot)
		want   string
	}{
		{
			name: "schema",
			mutate: func(snapshot *DoctorSnapshot) {
				snapshot.SchemaVersion = "mds.doctor/v0"
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

func TestTargetIdentityCompletenessKeepsPartialTargetsBlocked(t *testing.T) {
	id, err := target.NewID(target.KindLimaGuest, "mds")
	if err != nil {
		t.Fatalf("NewID(): %v", err)
	}
	facts := target.Facts{
		ID: id, OS: "linux", OSVersion: "26.04", Architecture: "arm64",
		RuntimeVersion: "1.0.0", ImageRevision: "sha256:image",
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
	writeJSON(t, planPath, plan)
	writeJSON(t, doctorPath, report)
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
  doctor)
    /bin/cat %q
    exit %d
    ;;
  *)
    printf 'unexpected command: %%s\n' "$1" >&2
    exit 2
    ;;
esac
`, fixtureCLIRevision, planPath, doctorPath, doctorExit)
	if err := os.WriteFile(binaryPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake mds: %v", err)
	}

	bundle := filepath.Join(root, "bundle")
	manifest, err := Certify(context.Background(), CertifyRequest{
		MDSPath: binaryPath, TargetID: targetID.String(), OutputDir: bundle,
		Components: []string{"go"},
		Now:        func() time.Time { return time.Unix(1<<40, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("Certify(): %v", err)
	}
	expectedStatus := StatusVerified
	if !report.Ready {
		expectedStatus = StatusBlocked
	}
	if manifest.Status != expectedStatus {
		t.Fatalf("Certify() status = %q, want %q", manifest.Status, expectedStatus)
	}
	return bundle
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

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(want)) {
		t.Fatalf("error = %v, want substring %q", err, want)
	}
}
