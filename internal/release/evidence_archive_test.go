package release

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractEvidenceArtifactRequiresExactBoundedEntries(t *testing.T) {
	t.Run("extracts exact bundle", func(t *testing.T) {
		archive := filepath.Join(t.TempDir(), "artifact.zip")
		writeEvidenceArtifactFixture(t, archive, "", nil)
		output := filepath.Join(t.TempDir(), "bundle")
		if err := ExtractEvidenceArtifact(archive, output); err != nil {
			t.Fatalf("ExtractEvidenceArtifact(): %v", err)
		}
		for _, name := range evidenceArchiveEntries {
			data, err := os.ReadFile(filepath.Join(output, name))
			if err != nil {
				t.Fatalf("read extracted %s: %v", name, err)
			}
			if string(data) != "fixture-"+name+"\n" {
				t.Fatalf("extracted %s = %q", name, data)
			}
		}
	})

	t.Run("rejects extra entry", func(t *testing.T) {
		archive := filepath.Join(t.TempDir(), "artifact.zip")
		writeEvidenceArtifactFixture(
			t,
			archive,
			"unexpected.txt",
			[]byte("unexpected"),
		)
		err := ExtractEvidenceArtifact(archive, filepath.Join(t.TempDir(), "bundle"))
		if err == nil || !strings.Contains(err.Error(), "5 entries, want 4") {
			t.Fatalf("ExtractEvidenceArtifact(extra) error = %v", err)
		}
	})

	t.Run("rejects oversized entry", func(t *testing.T) {
		archive := filepath.Join(t.TempDir(), "artifact.zip")
		writeEvidenceArtifactFixture(
			t,
			archive,
			evidenceArchiveEntries[0],
			bytes.Repeat([]byte("x"), int(maximumEvidenceEntryBytes)+1),
		)
		err := ExtractEvidenceArtifact(archive, filepath.Join(t.TempDir(), "bundle"))
		if err == nil || !strings.Contains(err.Error(), "oversized entry") {
			t.Fatalf("ExtractEvidenceArtifact(oversized) error = %v", err)
		}
	})

	t.Run("rejects oversized archive before parsing", func(t *testing.T) {
		archive := filepath.Join(t.TempDir(), "artifact.zip")
		if err := os.WriteFile(archive, []byte("not a zip"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(archive, maximumEvidenceArchiveBytes+1); err != nil {
			t.Fatal(err)
		}
		err := ExtractEvidenceArtifact(archive, filepath.Join(t.TempDir(), "bundle"))
		if err == nil || !strings.Contains(err.Error(), "size limit") {
			t.Fatalf("ExtractEvidenceArtifact(oversized archive) error = %v", err)
		}
	})
}

func writeEvidenceArtifactFixture(
	t *testing.T,
	path,
	replacementName string,
	replacement []byte,
) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, name := range evidenceArchiveEntries {
		data := []byte("fixture-" + name + "\n")
		if replacementName == name {
			data = replacement
		}
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if replacementName != "" && !containsEvidenceArchiveEntry(replacementName) {
		entry, err := writer.Create(replacementName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(replacement); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func containsEvidenceArchiveEntry(name string) bool {
	for _, expected := range evidenceArchiveEntries {
		if name == expected {
			return true
		}
	}
	return false
}
