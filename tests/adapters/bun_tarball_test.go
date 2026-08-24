package adapters_test

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/packages"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func TestNormalAndUpdateBunAdapterInstallOnlyVerifiedLocalTarball(t *testing.T) {
	content := []byte("reviewed npm tarball")
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/tool/-/tool-1.2.3.tgz" {
			t.Fatalf("download path = %q", request.URL.Path)
		}
		_, _ = writer.Write(content)
	}))
	defer server.Close()

	for _, mode := range []struct {
		name         string
		allowReplace bool
	}{
		{name: "normal apply"},
		{name: "update apply", allowReplace: true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			port := &recordingPort{
				err: func(command transport.Command) error {
					if len(command.Arguments) != 3 {
						t.Fatalf("Bun arguments = %v", command.Arguments)
					}
					localArtifact := command.Arguments[2]
					if !filepath.IsAbs(localArtifact) ||
						filepath.Ext(localArtifact) != ".tgz" {
						t.Fatalf("local artifact argument = %q", localArtifact)
					}
					if strings.Contains(localArtifact, "tool@1.2.3") {
						t.Fatalf("Bun re-resolves package version: %q", localArtifact)
					}
					downloaded, err := os.ReadFile(localArtifact)
					if err != nil {
						t.Fatalf("read verified local artifact: %v", err)
					}
					if string(downloaded) != string(content) {
						t.Fatalf("downloaded content = %q", downloaded)
					}
					return nil
				},
			}
			adapter := packages.Adapter{
				Home: t.TempDir(),
				Port: port,
				Environment: bunFixtureEnvironment(
					server.URL+"/tool/-/tool-1.2.3.tgz",
					content,
				),
				AllowReplace: mode.allowReplace,
				Vendor:       packages.Vendor{Client: server.Client()},
			}
			if err := adapter.Apply(context.Background(), bunFixtureAction()); err != nil {
				t.Fatalf("Apply(): %v", err)
			}
			if len(port.commands) != 1 ||
				port.commands[0].Executable != filepath.Join(adapter.Home, ".local", "bin", "bun") ||
				port.commands[0].Arguments[0] != "add" ||
				port.commands[0].Arguments[1] != "--global" {
				t.Fatalf("commands = %+v", port.commands)
			}
		})
	}
}

func TestBunAdapterReplacesExistingManagedGlobalDependencyBeforeInstall(t *testing.T) {
	content := []byte("reviewed npm tarball")
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		_, _ = writer.Write(content)
	}))
	defer server.Close()

	home := t.TempDir()
	manifestPath := filepath.Join(
		home, ".local", "share", "bun", "install", "global", "package.json",
	)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		manifestPath,
		[]byte(`{"dependencies":{"tool":"/tmp/old-tool.tgz"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	port := &recordingPort{}
	adapter := packages.Adapter{
		Home: home,
		Port: port,
		Environment: bunFixtureEnvironment(
			server.URL+"/tool/-/tool-1.2.3.tgz",
			content,
		),
		Vendor: packages.Vendor{Client: server.Client()},
	}
	if err := adapter.Apply(context.Background(), bunFixtureAction()); err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	if len(port.commands) != 2 {
		t.Fatalf("commands = %+v", port.commands)
	}
	if got, want := port.commands[0].Arguments, []string{"remove", "--global", "tool"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("remove arguments = %v, want %v", got, want)
	}
	if got := port.commands[1].Arguments; len(got) != 3 ||
		got[0] != "add" || got[1] != "--global" || !filepath.IsAbs(got[2]) {
		t.Fatalf("add arguments = %v", got)
	}
}

func TestBunAdapterRejectsStoredIntegrityOrDigestSubstitution(t *testing.T) {
	content := []byte("served tarball")
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		_, _ = writer.Write(content)
	}))
	defer server.Close()

	for _, test := range []struct {
		name   string
		mutate func(*catalog.NPMArtifact)
		want   string
	}{
		{
			name: "integrity",
			mutate: func(artifact *catalog.NPMArtifact) {
				artifact.Integrity = bunFixtureSRI([]byte("different tarball"))
			},
			want: "integrity mismatch",
		},
		{
			name: "sha256 digest",
			mutate: func(artifact *catalog.NPMArtifact) {
				artifact.SHA256 = strings.Repeat("0", 64)
			},
			want: "digest mismatch",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			environment := bunFixtureEnvironment(server.URL+"/tool.tgz", content)
			entry := environment.Lock.Versions["tool"]
			test.mutate(entry.NPM)
			environment.Lock.Versions["tool"] = entry
			port := &recordingPort{}
			adapter := packages.Adapter{
				Home: t.TempDir(), Port: port, Environment: environment,
				Vendor: packages.Vendor{Client: server.Client()},
			}
			err := adapter.Apply(context.Background(), bunFixtureAction())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Apply() error = %v, want %q", err, test.want)
			}
			if len(port.commands) != 0 {
				t.Fatalf("Bun ran before verification: %+v", port.commands)
			}
		})
	}
}

func bunFixtureEnvironment(tarball string, content []byte) catalog.Environment {
	sum := sha256.Sum256(content)
	return catalog.Environment{
		Catalog: catalog.Catalog{Components: []catalog.Component{
			{
				ID: "tool", Kind: "agent",
				VersionPolicy: catalog.VersionPolicy{
					Mode: "pinned", LockKey: "tool",
				},
			},
		}},
		Lock: catalog.VersionLock{Versions: map[string]catalog.LockEntry{
			"tool": {
				Version: "1.2.3",
				NPM: &catalog.NPMArtifact{
					Tarball: tarball, Integrity: bunFixtureSRI(content),
					SHA256: hex.EncodeToString(sum[:]),
				},
			},
		}},
	}
}

func bunFixtureAction() planning.Action {
	return planning.Action{
		ID: "lima-guest:mds/tool", ComponentID: "tool",
		Installer: "bun", Package: "tool", Version: "1.2.3",
	}
}

func bunFixtureSRI(content []byte) string {
	sum := sha512.Sum512(content)
	return "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
}
