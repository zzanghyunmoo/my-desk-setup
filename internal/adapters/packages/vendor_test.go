package packages

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
)

func TestVendorInstallPublishesNativeAgentAtStableCommandName(t *testing.T) {
	payload := []byte("native codex fixture\n")
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	entry, err := writer.Create("codex-aarch64-apple-darwin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		_ *http.Request,
	) {
		_, _ = response.Write(archive.Bytes())
	}))
	defer server.Close()
	archiveDigest := sha256.Sum256(archive.Bytes())
	payloadDigest := sha256.Sum256(payload)
	component := catalog.Component{
		ID: "codex", Kind: "agent",
		Verification: catalog.Verification{Command: []string{"codex", "--version"}},
	}
	lock := catalog.LockEntry{
		Artifacts: map[string]catalog.Artifact{"darwin-arm64": {
			URL: server.URL + "/codex.zip", Format: "zip",
			SHA256:           hex.EncodeToString(archiveDigest[:]),
			Executable:       "codex-aarch64-apple-darwin",
			ExecutableSHA256: hex.EncodeToString(payloadDigest[:]),
		}},
	}
	home := t.TempDir()
	if err := (Vendor{
		Client: server.Client(), Home: home, Platform: "darwin", Arch: "arm64",
	}).Install(context.Background(), component, lock); err != nil {
		t.Fatalf("Install(): %v", err)
	}
	installed, err := os.ReadFile(filepath.Join(home, ".local", "bin", "codex"))
	if err != nil || !bytes.Equal(installed, payload) {
		t.Fatalf("stable native agent content=%q err=%v", installed, err)
	}
	if _, err := os.Lstat(filepath.Join(
		home, ".local", "bin", "codex-aarch64-apple-darwin",
	)); !os.IsNotExist(err) {
		t.Fatalf("archive filename leaked into command path: %v", err)
	}
}

func TestVendorAgentExecutableNameUsesStableVerificationCommand(t *testing.T) {
	component := catalog.Component{
		ID: "codex", Kind: "agent",
		Verification: catalog.Verification{Command: []string{"codex", "--version"}},
	}
	artifact := catalog.Artifact{Executable: "codex-aarch64-apple-darwin"}

	if got, err := vendorExecutableName(component, artifact, "darwin"); err != nil || got != "codex" {
		t.Fatalf("darwin agent executable = %q, err=%v", got, err)
	}
	if got, err := vendorExecutableName(component, artifact, "windows"); err != nil || got != "codex.exe" {
		t.Fatalf("windows agent executable = %q, err=%v", got, err)
	}
}

func TestVendorExecutableDigestRejectsChangedNativePayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(path, []byte("changed payload"), 0o700); err != nil {
		t.Fatal(err)
	}
	artifact := catalog.Artifact{ExecutableSHA256: strings.Repeat("a", 64)}
	digest, err := exactVendorExecutableDigest(path, artifact)
	if err == nil || digest == artifact.ExecutableSHA256 || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("exactVendorExecutableDigest() digest=%q err=%v", digest, err)
	}
}

func TestReviewedHTTPClientRejectsCredentialBearingURL(t *testing.T) {
	if _, err := ReviewedHTTPClient(
		nil,
		"https://user:password@example.com/artifact",
		5*time.Minute,
	); err == nil || !strings.Contains(err.Error(), "credential-free") {
		t.Fatalf("reviewedHTTPClient() error = %v, want credential rejection", err)
	}
}

func TestReviewedHTTPClientRejectsQueryBearingURL(t *testing.T) {
	if _, err := ReviewedHTTPClient(
		nil,
		"https://example.com/artifact?token=secret",
		5*time.Minute,
	); err == nil || !strings.Contains(err.Error(), "query") {
		t.Fatalf("ReviewedHTTPClient() error = %v, want query rejection", err)
	}
}

func TestReviewedHTTPClientRejectsCrossOriginRedirect(t *testing.T) {
	client, err := ReviewedHTTPClient(
		nil,
		"https://example.com/artifact",
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("reviewedHTTPClient(): %v", err)
	}
	request := &http.Request{URL: &url.URL{
		Scheme: "https",
		Host:   "cdn.example.net",
		Path:   "/artifact",
	}}
	if err := client.CheckRedirect(request, nil); err == nil ||
		!strings.Contains(err.Error(), "cross-origin") {
		t.Fatalf("CheckRedirect() error = %v, want cross-origin rejection", err)
	}
}

func TestReviewedHTTPClientRejectsCredentialBearingRedirect(t *testing.T) {
	client, err := ReviewedHTTPClient(
		nil,
		"https://example.com/artifact",
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("ReviewedHTTPClient(): %v", err)
	}
	request := &http.Request{URL: &url.URL{
		Scheme: "https",
		Host:   "example.com",
		User:   url.UserPassword("token", "secret"),
		Path:   "/artifact",
	}}
	if err := client.CheckRedirect(request, nil); err == nil ||
		!strings.Contains(err.Error(), "cross-origin") {
		t.Fatalf(
			"CheckRedirect() error = %v, want credential-bearing redirect rejection",
			err,
		)
	}
}

func TestReviewedReleaseHTTPClientAllowsOnlyThreeCredentialFreeHTTPSRedirects(
	t *testing.T,
) {
	client, err := reviewedReleaseHTTPClient(
		nil,
		"https://github.com/example/tool/releases/download/v1/tool.tar.gz",
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("reviewedReleaseHTTPClient(): %v", err)
	}
	redirect := &http.Request{
		URL: &url.URL{
			Scheme:   "https",
			Host:     "release-assets.githubusercontent.com",
			Path:     "/tool.tar.gz",
			RawQuery: "signature=temporary",
		},
		Header: http.Header{
			"Authorization":       {"Bearer secret"},
			"Cookie":              {"session=secret"},
			"Proxy-Authorization": {"Basic secret"},
		},
	}
	if err := client.CheckRedirect(
		redirect,
		make([]*http.Request, 3),
	); err != nil {
		t.Fatalf("third redirect rejected: %v", err)
	}
	for _, header := range []string{
		"Authorization",
		"Cookie",
		"Proxy-Authorization",
	} {
		if redirect.Header.Get(header) != "" {
			t.Fatalf("redirect retained %s", header)
		}
	}
	if err := client.CheckRedirect(
		redirect,
		make([]*http.Request, 4),
	); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("fourth redirect error = %v, want bounded rejection", err)
	}

	redirect.URL.User = url.UserPassword("user", "password")
	if err := client.CheckRedirect(
		redirect,
		make([]*http.Request, 1),
	); err == nil || !strings.Contains(err.Error(), "credential-free") {
		t.Fatalf("credentialed redirect error = %v", err)
	}
}
