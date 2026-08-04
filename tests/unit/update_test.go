package unit_test

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	catalogdata "github.com/zzanghyunmoo/my-desk-setup/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/cli"
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
	if got, want := len(first.CompatibilityMatrix), 6; got != want {
		t.Fatalf("compatibility matrix entries = %d, want %d", got, want)
	}
	for _, entry := range first.CompatibilityMatrix {
		if entry.PlanDigest == "" ||
			(entry.TargetKind != catalog.TargetMacOSHost &&
				entry.TargetKind != catalog.TargetWSLGuest &&
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
		"credentialed provenance": {
			ComponentID: "typescript", Version: "6.0.3", Source: "npm",
			Provenance: "https://token@example.com/typescript/6.0.3",
		},
		"query-bearing provenance": {
			ComponentID: "typescript", Version: "6.0.3", Source: "npm",
			Provenance: "https://example.com/typescript/6.0.3?token=secret",
		},
		"credentialed artifact": {
			ComponentID: "bun", Version: "99.0.0", Source: "fixture",
			Provenance: "https://example.com/bun/99.0.0",
			Artifacts: map[string]catalog.Artifact{
				"linux-amd64": {
					URL:    "https://token@example.com/bun.zip",
					SHA256: strings.Repeat("a", 64),
					Format: "zip", Executable: "bun",
				},
			},
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
		"non-canonical artifact checksum": {
			ComponentID: "bun", Version: "99.0.0", Source: "fixture",
			Provenance: "https://example.com/bun/99.0.0",
			Artifacts: map[string]catalog.Artifact{
				"linux-amd64": {
					URL:    "https://example.com/bun.zip",
					SHA256: strings.Repeat("A", 64),
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

func TestUpdateRejectsMiseManagedCandidateBeforeMutation(t *testing.T) {
	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		t.Fatalf("LoadFS(): %v", err)
	}
	current := environment.Lock.Versions["go"]
	id, _ := target.NewID(target.KindLimaGuest, "mds")
	_, _, err = updateflow.Build(
		environment,
		target.Facts{
			ID: id, OS: "linux", OSVersion: "26.04",
			Architecture: "arm64", Reachable: true,
		},
		updateflow.Candidate{
			ComponentID: "go",
			Version:     "99.0.0",
			Source:      current.Source,
			Provenance:  current.Provenance,
			Artifacts:   current.Artifacts,
		},
	)
	if got := updateflow.KindOf(err); got != updateflow.ErrorInvalid ||
		!strings.Contains(err.Error(), "mise.toml") ||
		!strings.Contains(err.Error(), "one transaction") {
		t.Fatalf("Build(mise candidate) error = %v kind=%q", err, got)
	}
	if environment.Lock.Versions["go"].Version != current.Version {
		t.Fatal("rejected mise update mutated the input environment")
	}
}

func TestUpdateErrorsClassifyLocalValidationAndStalePlan(t *testing.T) {
	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		t.Fatalf("LoadFS(): %v", err)
	}
	id, _ := target.NewID(target.KindLimaGuest, "mds")
	_, _, err = updateflow.Build(
		environment,
		target.Facts{ID: id, OS: "linux", Architecture: "arm64"},
		updateflow.Candidate{
			ComponentID: "typescript",
			Version:     "6.0.3",
			Source:      "npm registry",
			Provenance:  "http://example.com/typescript/6.0.3",
		},
	)
	if got := updateflow.KindOf(err); got != updateflow.ErrorInvalid {
		t.Fatalf("Build() kind = %q, want %q; err=%v", got, updateflow.ErrorInvalid, err)
	}

	plan := updateflow.Plan{SchemaVersion: updateflow.PlanSchema}
	if err := updateflow.Verify(plan, "sha256:not-reviewed"); err == nil {
		t.Fatal("Verify() accepted incomplete plan")
	} else if got := updateflow.KindOf(err); got != updateflow.ErrorStale {
		t.Fatalf("Verify() kind = %q, want %q; err=%v", got, updateflow.ErrorStale, err)
	}
	versionOne := updateflow.Plan{SchemaVersion: "mds.update/v1"}
	if err := updateflow.Verify(versionOne, "sha256:not-reviewed"); err == nil ||
		!strings.Contains(err.Error(), "unsupported update plan schema") {
		t.Fatalf("Verify(v1) error = %v", err)
	}

	_, err = updateflow.Discover(
		context.Background(),
		environment,
		catalog.TargetLimaGuest,
		"herdr",
		nil,
		"",
	)
	if got := updateflow.KindOf(err); got != updateflow.ErrorInvalid {
		t.Fatalf("Discover(local validation) kind = %q, want %q; err=%v", got, updateflow.ErrorInvalid, err)
	}
}

func TestCLIUpdateMapsLocalDiscoveryValidationToInvalidInput(t *testing.T) {
	var stdout strings.Builder
	var stderr strings.Builder
	code := cli.Run(
		[]string{
			"update",
			"--catalog", filepath.Join("..", "..", "catalog"),
			"--component", "herdr",
			"--format", "json",
		},
		cli.Streams{
			Input: strings.NewReader(""), Output: &stdout, Error: &stderr,
		},
		cli.Runtime{
			GOOS: "linux", GOARCH: "arm64",
			Getenv: func(key string) string {
				if key == "LIMA_INSTANCE" {
					return "mds"
				}
				return ""
			},
		},
	)
	if code != cli.ExitInvalidInput {
		t.Fatalf(
			"Run() code=%d, want invalid-input %d; stderr=%q",
			code,
			cli.ExitInvalidInput,
			stderr.String(),
		)
	}
	var envelope cli.ErrorEnvelope
	if err := json.Unmarshal([]byte(stderr.String()), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v\n%s", err, stderr.String())
	}
	if envelope.Code != "invalid-input" {
		t.Fatalf("error envelope = %+v, want invalid-input", envelope)
	}
}

func TestCLIUpdateRequiresWritableCheckoutCatalog(t *testing.T) {
	var stdout strings.Builder
	var stderr strings.Builder
	code := cli.Run(
		[]string{
			"update",
			"--component", "typescript",
			"--format", "json",
		},
		cli.Streams{
			Input: strings.NewReader(""), Output: &stdout, Error: &stderr,
		},
		cli.Runtime{
			GOOS: "linux", GOARCH: "arm64",
			Getenv: func(string) string { return "" },
		},
	)
	if code != cli.ExitInvalidInput {
		t.Fatalf(
			"Run() code=%d, want invalid-input %d; stderr=%q",
			code,
			cli.ExitInvalidInput,
			stderr.String(),
		)
	}
	if !strings.Contains(stderr.String(), "embedded catalog data is read-only") {
		t.Fatalf("stderr=%q, want read-only embedded catalog contract", stderr.String())
	}
}

func TestUpdateDiscoveryRejectsCrossOriginRedirect(t *testing.T) {
	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		t.Fatalf("LoadFS(): %v", err)
	}
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		redirectTarget := strings.Replace(server.URL, "127.0.0.1", "localhost", 1) +
			request.URL.Path
		http.Redirect(writer, request, redirectTarget, http.StatusFound)
	}))
	defer server.Close()

	_, err = updateflow.Discover(
		context.Background(),
		environment,
		catalog.TargetLimaGuest,
		"typescript",
		server.Client(),
		server.URL,
	)
	if err == nil || !strings.Contains(err.Error(), "cross-origin redirect") {
		t.Fatalf("Discover(cross-origin redirect) error = %v", err)
	}
}

func TestUpdateDiscoveryRejectsCrossOriginTarballRedirect(t *testing.T) {
	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		t.Fatalf("LoadFS(): %v", err)
	}
	content := []byte("reviewed tarball")
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/typescript/latest":
			payload := map[string]any{
				"version": "6.0.3",
				"dist": map[string]string{
					"integrity": fixtureSRI(content),
					"tarball": server.URL +
						"/typescript/-/typescript-6.0.3.tgz",
				},
			}
			if err := json.NewEncoder(writer).Encode(payload); err != nil {
				t.Fatalf("encode metadata: %v", err)
			}
		case "/typescript/-/typescript-6.0.3.tgz":
			redirectTarget := strings.Replace(
				server.URL,
				"127.0.0.1",
				"localhost",
				1,
			) + request.URL.Path
			http.Redirect(writer, request, redirectTarget, http.StatusFound)
		default:
			t.Fatalf("unexpected request path: %s", request.URL.Path)
		}
	}))
	defer server.Close()

	_, err = updateflow.Discover(
		context.Background(),
		environment,
		catalog.TargetLimaGuest,
		"typescript",
		server.Client(),
		server.URL,
	)
	if err == nil || !strings.Contains(err.Error(), "cross-origin redirect") {
		t.Fatalf("Discover(cross-origin tarball redirect) error = %v", err)
	}
}

func TestUpdateDiscoveryBoundsMetadataAndRedactsTransportErrors(t *testing.T) {
	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		t.Fatalf("LoadFS(): %v", err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		_, _ = io.WriteString(
			writer,
			`{"version":"6.0.3","dist":{"integrity":"ignored","tarball":"ignored"},"extra":"`,
		)
		_, _ = io.WriteString(writer, strings.Repeat("a", (1<<20)+1))
		_, _ = io.WriteString(writer, `"}`)
	}))
	defer server.Close()
	_, err = updateflow.Discover(
		context.Background(),
		environment,
		catalog.TargetLimaGuest,
		"typescript",
		server.Client(),
		server.URL,
	)
	if err == nil || !strings.Contains(err.Error(), "exceeds 1 MiB") {
		t.Fatalf("Discover(oversized metadata) error = %v", err)
	}

	const secret = "super-secret-token"
	client := &http.Client{Transport: roundTripFunc(func(
		*http.Request,
	) (*http.Response, error) {
		return nil, errors.New("dial https://" + secret + "@registry.example")
	})}
	_, err = updateflow.Discover(
		context.Background(),
		environment,
		catalog.TargetLimaGuest,
		"typescript",
		client,
		"https://registry.example",
	)
	if err == nil {
		t.Fatal("Discover(transport failure) succeeded")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("Discover() leaked credential in error: %v", err)
	}
	if got := updateflow.KindOf(err); got != updateflow.ErrorUnreachable {
		t.Fatalf("Discover() kind = %q, want %q; err=%v", got, updateflow.ErrorUnreachable, err)
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
	server := httptest.NewTLSServer(http.HandlerFunc(func(
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
		catalog.TargetMacOSHost,
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
		catalog.TargetWindowsHost,
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return function(request)
}
