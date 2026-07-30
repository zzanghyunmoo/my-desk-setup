package integration_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	catalogdata "github.com/zzanghyunmoo/my-desk-setup/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
)

func TestPlanDoesNotMutateStateOrTargetPreimage(t *testing.T) {
	stateRoot := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(stateRoot, "existing-receipt.json"),
		[]byte(`{"status":"ready"}`),
		0o600,
	); err != nil {
		t.Fatalf("write fixture state: %v", err)
	}
	beforeState := snapshot(t, stateRoot)

	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		t.Fatalf("LoadFS(): %v", err)
	}
	id, _ := target.NewID(target.KindLimaGuest, "mds")
	facts := target.Facts{
		ID: id, OS: "linux", OSVersion: "26.04", Architecture: "arm64",
		ImageRevision: "sha256:fixture", SystemdSupported: true,
		SystemdActive: true, Reachable: true,
	}
	beforeFacts := facts

	if _, err := planning.Build(environment, facts, planning.All()); err != nil {
		t.Fatalf("Build(): %v", err)
	}
	afterState := snapshot(t, stateRoot)
	if !reflect.DeepEqual(beforeState, afterState) {
		t.Fatalf("state changed during plan: before=%v after=%v", beforeState, afterState)
	}
	if !reflect.DeepEqual(facts, beforeFacts) {
		t.Fatalf("target facts mutated: before=%+v after=%+v", beforeFacts, facts)
	}
}

func snapshot(t *testing.T, root string) []string {
	t.Helper()
	var entries []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entries = append(entries, fmt.Sprintf("%s:%x", relative, sum))
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	sort.Strings(entries)
	return entries
}
