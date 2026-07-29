package unit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestUpdateDiscoversExactNPMCandidateWithoutMutation(t *testing.T) {
	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		t.Fatalf("LoadFS(): %v", err)
	}
	before := environment.Lock.Versions["typescript"]
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/typescript/latest" {
			t.Fatalf("request path = %q", request.URL.Path)
		}
		_, _ = writer.Write([]byte(
			`{"name":"typescript","version":"6.0.3","dist":{"integrity":"sha512-reviewed","tarball":"https://registry.npmjs.org/typescript/-/typescript-6.0.3.tgz"},"extra":"ignored"}`,
		))
	}))
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
		!strings.Contains(candidate.Provenance, "/typescript/v/6.0.3") {
		t.Fatalf("candidate = %+v", candidate)
	}
	if !reflect.DeepEqual(environment.Lock.Versions["typescript"], before) {
		t.Fatal("candidate discovery mutated the lock")
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
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.EscapedPath() != "/@anthropic-ai%2Fclaude-code/latest" {
			t.Fatalf("escaped request path = %q", request.URL.EscapedPath())
		}
		_, _ = writer.Write([]byte(
			`{"version":"99.0.0","dist":{"integrity":"sha512-reviewed","tarball":"https://registry.npmjs.org/@anthropic-ai/claude-code/-/claude-code-99.0.0.tgz"}}`,
		))
	}))
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
		!strings.Contains(candidate.Provenance, "/@anthropic-ai/claude-code/v/99.0.0") {
		t.Fatalf("candidate = %+v", candidate)
	}
}
