package unit_test

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	catalogdata "github.com/zzanghyunmoo/my-desk-setup/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	updateflow "github.com/zzanghyunmoo/my-desk-setup/internal/update"
)

func TestUpdatePlanIsDeterministicAndDoesNotMutateInput(t *testing.T) {
	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		t.Fatalf("LoadFS(): %v", err)
	}
	beforeVersion := environment.Lock.Versions["typescript"].Version
	id, _ := target.NewID(target.KindLimaGuest, "mds")
	facts := target.Facts{
		ID: id, OS: "linux", OSVersion: "26.04", Architecture: "arm64",
		SystemdSupported: true, SystemdActive: true, Reachable: true,
		CLIRevision: "dev",
	}
	candidate := updateflow.Candidate{
		ComponentID: "typescript", Version: "6.0.3",
		Source:     "npm registry",
		Provenance: "https://www.npmjs.com/package/typescript/v/6.0.3",
		NPM:        fixtureNPMArtifact("typescript", "6.0.3", []byte("typescript-6.0.3")),
	}
	first, updated, err := updateflow.Build(environment, facts, candidate)
	if err != nil {
		t.Fatalf("Build(first): %v", err)
	}
	second, _, err := updateflow.Build(environment, facts, candidate)
	if err != nil {
		t.Fatalf("Build(second): %v", err)
	}
	if first.Digest != second.Digest {
		t.Fatalf("update digest changed: %s != %s", first.Digest, second.Digest)
	}
	if first.Old.Version != beforeVersion || first.New.Version != "6.0.3" {
		t.Fatalf("version diff = %s -> %s", first.Old.Version, first.New.Version)
	}
	if first.New.Source != candidate.Source ||
		first.New.Provenance != candidate.Provenance ||
		!reflect.DeepEqual(first.New.NPM, candidate.NPM) {
		t.Fatalf("new lock lost candidate source/provenance/npm: %+v", first.New)
	}
	tampered := first
	tamperedNPM := *tampered.New.NPM
	tamperedNPM.SHA256 = strings.Repeat("0", 64)
	tampered.New.NPM = &tamperedNPM
	if err := updateflow.Verify(tampered, first.Digest); err == nil ||
		!strings.Contains(err.Error(), "payload digest mismatch") {
		t.Fatalf("Verify(tampered NPM digest) error = %v", err)
	}
	if environment.Lock.Versions["typescript"].Version != beforeVersion {
		t.Fatal("Build mutated input environment")
	}
	if updated.Lock.Versions["typescript"].Version != "6.0.3" {
		t.Fatal("Build did not return updated environment")
	}
	if first.TargetPlan.Digest == "" ||
		first.TargetPlan.CatalogRevision != first.AfterCatalogRevision {
		t.Fatalf("resulting target plan = %+v", first.TargetPlan)
	}
	if got, want := len(first.CompatibilityMatrix), 4; got != want {
		t.Fatalf("compatibility matrix entries = %d, want %d", got, want)
	}
	for _, entry := range first.CompatibilityMatrix {
		if entry.PlanDigest == "" ||
			(entry.TargetKind != catalog.TargetWSLGuest &&
				entry.TargetKind != catalog.TargetLimaGuest) {
			t.Fatalf("unexpected compatibility entry: %+v", entry)
		}
	}
}

func TestUpdateRejectsNoChangeAndUnknownCandidateFields(t *testing.T) {
	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		t.Fatalf("LoadFS(): %v", err)
	}
	current := environment.Lock.Versions["typescript"]
	id, _ := target.NewID(target.KindLimaGuest, "mds")
	_, _, err = updateflow.Build(
		environment,
		target.Facts{ID: id, OS: "linux", Architecture: "arm64"},
		updateflow.Candidate{
			ComponentID: "typescript", Version: current.Version,
			Source: current.Source, Provenance: current.Provenance,
			NPM: current.NPM,
		},
	)
	if err == nil || !strings.Contains(err.Error(), "does not change") {
		t.Fatalf("Build(no change) error = %v", err)
	}
	if _, err := updateflow.DecodeCandidate([]byte(
		`{"component_id":"typescript","version":"6.0.3","source":"npm","provenance":"https://example.com","token":"forbidden"}`,
	)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("DecodeCandidate(unknown) error = %v", err)
	}
	for name, candidate := range map[string]updateflow.Candidate{
		"insecure provenance": {
			ComponentID: "typescript", Version: "6.0.3", Source: "npm",
			Provenance: "http://example.com/typescript/6.0.3",
		},
		"invalid artifact checksum": {
			ComponentID: "bun", Version: "99.0.0", Source: "fixture",
			Provenance: "https://example.com/bun/99.0.0",
			Artifacts: map[string]catalog.Artifact{
				"linux-amd64": {
					URL: "https://example.com/bun.zip", SHA256: "not-a-sha256",
					Format: "zip", Executable: "bun",
				},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := updateflow.Build(
				environment,
				target.Facts{ID: id, OS: "linux", Architecture: "amd64"},
				candidate,
			); err == nil {
				t.Fatalf("Build(%s) succeeded", name)
			}
		})
	}
}

func TestUpdateRejectsIncompleteVendorTargetArchitectureMatrix(t *testing.T) {
	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		t.Fatalf("LoadFS(): %v", err)
	}
	id, _ := target.NewID(target.KindLimaGuest, "mds")
	_, _, err = updateflow.Build(
		environment,
		target.Facts{
			ID: id, OS: "linux", OSVersion: "26.04",
			Architecture: "amd64", Reachable: true,
		},
		updateflow.Candidate{
			ComponentID: "bun",
			Version:     "99.0.0",
			Source:      "fixture",
			Provenance:  "https://example.com/bun/99.0.0",
			Artifacts: map[string]catalog.Artifact{
				"linux-amd64": {
					URL:    "https://example.com/bun.zip",
					SHA256: strings.Repeat("a", 64),
					Format: "zip", Executable: "bun",
				},
			},
		},
	)
	if err == nil ||
		!strings.Contains(err.Error(), "compatibility matrix") ||
		!strings.Contains(err.Error(), "darwin-amd64") {
		t.Fatalf("Build(incomplete matrix) error = %v", err)
	}
}

func TestUpdateDiscoversExactNPMCandidateWithoutMutation(t *testing.T) {
	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		t.Fatalf("LoadFS(): %v", err)
	}
	before := environment.Lock.Versions["typescript"]
	content := []byte("reviewed typescript tarball")
	server := newNPMRegistry(
		t,
		"typescript",
		"6.0.3",
		content,
		fixtureSRI(content),
		"",
	)
	defer server.Close()
	candidate, err := updateflow.Discover(
		context.Background(),
		environment,
		catalog.TargetLimaGuest,
		"typescript",
		server.Client(),
		server.URL,
	)
	if err != nil {
		t.Fatalf("Discover(): %v", err)
	}
	if candidate.Version != "6.0.3" ||
		!strings.Contains(candidate.Provenance, "/typescript/v/6.0.3") ||
		candidate.NPM == nil ||
		candidate.NPM.Tarball != server.URL+"/typescript/-/typescript-6.0.3.tgz" {
		t.Fatalf("candidate = %+v", candidate)
	}
	sum := sha256.Sum256(content)
	if candidate.NPM.SHA256 != hex.EncodeToString(sum[:]) ||
		candidate.NPM.Integrity != fixtureSRI(content) {
		t.Fatalf("candidate NPM artifact = %+v", candidate.NPM)
	}
	if !reflect.DeepEqual(environment.Lock.Versions["typescript"], before) {
		t.Fatal("candidate discovery mutated the lock")
	}
}

func TestUpdateDiscoveryRejectsMetadataAndContentSubstitution(t *testing.T) {
	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		t.Fatalf("LoadFS(): %v", err)
	}
	reviewed := []byte("reviewed tarball")
	tests := []struct {
		name            string
		served          []byte
		integrity       string
		tarballOverride string
		want            string
	}{
		{
			name:   "metadata tarball URL",
			served: reviewed, integrity: fixtureSRI(reviewed),
			tarballOverride: "https://attacker.invalid/typescript.tgz",
			want:            "canonical",
		},
		{
			name:      "tarball content",
			served:    []byte("substituted tarball"),
			integrity: fixtureSRI(reviewed),
			want:      "integrity mismatch",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newNPMRegistry(
				t,
				"typescript",
				"6.0.3",
				test.served,
				test.integrity,
				test.tarballOverride,
			)
			defer server.Close()
			_, err := updateflow.Discover(
				context.Background(),
				environment,
				catalog.TargetLimaGuest,
				"typescript",
				server.Client(),
				server.URL,
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Discover() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestUpdateDiscoveryRateLimitAndUnsupportedProviderMutateNothing(t *testing.T) {
	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		t.Fatalf("LoadFS(): %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	if _, err := updateflow.Discover(
		context.Background(),
		environment,
		catalog.TargetLimaGuest,
		"typescript",
		server.Client(),
		server.URL,
	); err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("Discover(rate limited) error = %v", err)
	}
	if _, err := updateflow.Discover(
		context.Background(),
		environment,
		catalog.TargetLimaGuest,
		"herdr",
		server.Client(),
		server.URL,
	); err == nil || !strings.Contains(err.Error(), "reviewed candidate file") {
		t.Fatalf("Discover(vendor) error = %v", err)
	}
}

func TestUpdateDiscoveryEscapesScopedNPMPackage(t *testing.T) {
	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		t.Fatalf("LoadFS(): %v", err)
	}
	content := []byte("reviewed scoped package")
	server := newNPMRegistry(
		t,
		"@anthropic-ai/claude-code",
		"99.0.0",
		content,
		fixtureSRI(content),
		"",
	)
	defer server.Close()
	candidate, err := updateflow.Discover(
		context.Background(),
		environment,
		catalog.TargetLimaGuest,
		"claude-code",
		server.Client(),
		server.URL,
	)
	if err != nil {
		t.Fatalf("Discover(): %v", err)
	}
	if candidate.Version != "99.0.0" ||
		!strings.Contains(candidate.Provenance, "/@anthropic-ai/claude-code/v/99.0.0") ||
		candidate.NPM == nil ||
		!strings.Contains(candidate.NPM.Tarball, "/@anthropic-ai/claude-code/-/") {
		t.Fatalf("candidate = %+v", candidate)
	}
}

func fixtureNPMArtifact(
	packageName,
	version string,
	content []byte,
) *catalog.NPMArtifact {
	sum := sha256.Sum256(content)
	name := packageName
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	return &catalog.NPMArtifact{
		Tarball: "https://registry.npmjs.org/" + packageName + "/-/" +
			name + "-" + version + ".tgz",
		Integrity: fixtureSRI(content),
		SHA256:    hex.EncodeToString(sum[:]),
	}
}

func fixtureSRI(content []byte) string {
	sum := sha512.Sum512(content)
	return "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
}

func newNPMRegistry(
	t *testing.T,
	packageName,
	version string,
	content []byte,
	integrity,
	tarballOverride string,
) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		metadataPath := "/" + url.PathEscape(packageName) + "/latest"
		name := packageName
		if slash := strings.LastIndex(name, "/"); slash >= 0 {
			name = name[slash+1:]
		}
		tarballPath := "/" + packageName + "/-/" + name + "-" + version + ".tgz"
		switch {
		case request.URL.EscapedPath() == metadataPath:
			tarball := tarballOverride
			if tarball == "" {
				tarball = server.URL + tarballPath
			}
			payload := map[string]any{
				"name":    packageName,
				"version": version,
				"dist": map[string]string{
					"integrity": integrity,
					"tarball":   tarball,
				},
				"extra": "ignored",
			}
			if err := json.NewEncoder(writer).Encode(payload); err != nil {
				t.Fatalf("encode metadata: %v", err)
			}
		case request.URL.Path == tarballPath:
			_, _ = writer.Write(content)
		default:
			t.Fatalf("unexpected registry path: %s", request.URL.EscapedPath())
		}
	}))
	return server
}
