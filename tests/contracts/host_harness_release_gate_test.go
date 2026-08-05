package contracts_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
	"github.com/zzanghyunmoo/my-desk-setup/internal/harness"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func TestHostHarnessReleaseGateRunsActualMergedArtifactPreviewAndApply(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("actual release gate is run on the macOS release coordinator")
	}
	archivePath := os.Getenv("MDS_OMH_RELEASE_ARCHIVE")
	sidecarPath := os.Getenv("MDS_OMH_RELEASE_SIDECAR")
	if archivePath == "" || sidecarPath == "" {
		t.Skip("set MDS_OMH_RELEASE_ARCHIVE and MDS_OMH_RELEASE_SIDECAR")
	}

	identity := readReleaseFixtureIdentity(t)
	sidecar := readReleaseSidecar(t, sidecarPath)
	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("ReadFile(OMH archive): %v", err)
	}
	archiveDigest := sha256.Sum256(archiveBytes)
	if got := hex.EncodeToString(archiveDigest[:]); got != identity.Release.ArchiveSHA256 ||
		got != sidecar.Archive.SHA256 ||
		int64(len(archiveBytes)) != identity.Release.ArchiveSize ||
		int64(len(archiveBytes)) != sidecar.Archive.Size {
		t.Fatalf(
			"archive identity mismatch: digest=%s size=%d fixture=%+v sidecar=%+v",
			got,
			len(archiveBytes),
			identity.Release,
			sidecar.Archive,
		)
	}
	if sidecar.Source.Commit != identity.Release.SourceCommit ||
		sidecar.Source.Tree != identity.Release.SourceTree ||
		sidecar.CatalogRevision != identity.Release.CatalogRevision ||
		sidecar.Package.Tag != identity.Release.Tag ||
		sidecar.Package.Version != identity.Release.Version {
		t.Fatalf("sidecar does not match fixture: sidecar=%+v fixture=%+v", sidecar, identity)
	}

	environment := loadHostHarnessFixture(t)
	platform := "darwin-" + runtime.GOARCH
	nodeArtifact, ok := environment.Lock.Versions["omh-node-runtime"].Artifacts[platform]
	if !ok {
		t.Fatalf("Node fixture has no %s artifact", platform)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	nodeSnapshot, err := (artifact.Snapshotter{TempRoot: t.TempDir()}).Acquire(
		ctx,
		artifact.SnapshotRequest{
			URL:        nodeArtifact.URL,
			SHA256:     nodeArtifact.SHA256,
			Format:     nodeArtifact.Format,
			Executable: nodeArtifact.Executable,
		},
	)
	if err != nil {
		t.Fatalf("Acquire(exact Node): %v", err)
	}
	t.Cleanup(func() {
		if closeErr := nodeSnapshot.Close(); closeErr != nil {
			t.Errorf("Close(Node snapshot): %v", closeErr)
		}
	})
	omHSnapshot, err := (artifact.Snapshotter{
		TempRoot: t.TempDir(),
		Open: func(_ context.Context, _ string) (io.ReadCloser, error) {
			return os.Open(archivePath)
		},
	}).Acquire(ctx, artifact.SnapshotRequest{
		URL:        "fixture://merged-oh-my-harness",
		SHA256:     identity.Release.ArchiveSHA256,
		Format:     "tar.gz",
		Executable: "package/omh",
		ExtractAll: true,
	})
	if err != nil {
		t.Fatalf("Acquire(merged OMH): %v", err)
	}
	t.Cleanup(func() {
		if closeErr := omHSnapshot.Close(); closeErr != nil {
			t.Errorf("Close(OMH snapshot): %v", closeErr)
		}
	})

	root := t.TempDir()
	request := harness.Request{
		NodeExecutable: nodeSnapshot.Executable(),
		Entrypoint:     omHSnapshot.Path("package/dist/cli/main.js"),
		StateRoot:      filepath.Join(root, "state"),
		Home:           filepath.Join(root, "home"),
		ConfigRoot:     filepath.Join(root, "config"),
		TempRoot:       filepath.Join(root, "tmp"),
		Platform:       "darwin",
		Locale:         "C",
		Timeout:        2 * time.Minute,
	}
	for _, path := range []string{request.Home, request.ConfigRoot, request.TempRoot} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatalf("MkdirAll(%s): %v", filepath.Base(path), err)
		}
	}
	runner := harness.Runner{Port: transport.NewLocal()}
	preview, err := runner.Preview(ctx, request)
	if err != nil {
		t.Fatalf("Preview(actual merged OMH): %v", err)
	}
	if preview.Readiness != "preview" || preview.Digest == "" ||
		preview.CatalogRevision != identity.Release.CatalogRevision ||
		len(preview.SelectedAgents) != 0 {
		t.Fatalf("actual preview = %+v", preview)
	}
	applied, err := runner.Apply(ctx, request, preview.Digest)
	if err != nil {
		t.Fatalf("Apply(actual merged OMH): %v", err)
	}
	if applied.Status != "ready" || applied.Digest != preview.Digest ||
		applied.CatalogRevision != preview.CatalogRevision ||
		len(applied.SelectedAgents) != 0 {
		t.Fatalf("actual apply = %+v", applied)
	}
}

type releaseFixtureIdentity struct {
	Release struct {
		Version         string `json:"version"`
		Tag             string `json:"tag"`
		ArchiveSHA256   string `json:"archive_sha256"`
		ArchiveSize     int64  `json:"archive_size"`
		SourceCommit    string `json:"source_commit"`
		SourceTree      string `json:"source_tree"`
		CatalogRevision string `json:"catalog_revision"`
	} `json:"release"`
}

type releaseSidecarIdentity struct {
	Archive struct {
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	} `json:"archive"`
	Source struct {
		Commit string `json:"commit"`
		Tree   string `json:"tree"`
	} `json:"source"`
	Package struct {
		Tag     string `json:"tag"`
		Version string `json:"version"`
	} `json:"package"`
	CatalogRevision string `json:"catalogRevision"`
}

func readReleaseFixtureIdentity(t *testing.T) releaseFixtureIdentity {
	t.Helper()
	path := filepath.Join(
		repositoryRoot(t),
		"tests", "fixtures", "catalog", "host-harness", "release-identity.json",
	)
	var identity releaseFixtureIdentity
	decodeBoundedReleaseJSON(t, path, &identity)
	return identity
}

func readReleaseSidecar(t *testing.T, path string) releaseSidecarIdentity {
	t.Helper()
	var sidecar releaseSidecarIdentity
	decodeBoundedReleaseJSON(t, path, &sidecar)
	return sidecar
}

func decodeBoundedReleaseJSON(t *testing.T, path string, target any) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open(%s): %v", filepath.Base(path), err)
	}
	defer file.Close()
	if err := json.NewDecoder(io.LimitReader(file, 64<<20)).Decode(target); err != nil {
		t.Fatalf("Decode(%s): %v", filepath.Base(path), err)
	}
}
