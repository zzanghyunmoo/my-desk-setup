package state_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/state"
)

const (
	lockHelperMode = "MDS_TEST_LOCK_HELPER"
	lockHelperPath = "MDS_TEST_LOCK_PATH"
)

func TestWriterLockCanBeReacquiredAfterHolderIsKilled(t *testing.T) {
	if os.Getenv(lockHelperMode) == "1" {
		lock, err := state.Acquire(os.Getenv(lockHelperPath))
		if err != nil {
			fmt.Fprintf(os.Stderr, "acquire child lock: %v\n", err)
			os.Exit(2)
		}
		t.Cleanup(func() {
			if err := lock.Release(); err != nil {
				t.Errorf("Release(child): %v", err)
			}
		})
		if _, err := fmt.Fprintln(os.Stdout, "locked"); err != nil {
			t.Fatalf("announce child lock readiness: %v", err)
		}
		select {}
	}

	path := filepath.Join(t.TempDir(), "writer.lock")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		os.Args[0],
		"-test.run=^TestWriterLockCanBeReacquiredAfterHolderIsKilled$",
	)
	command.Env = append(
		os.Environ(),
		lockHelperMode+"=1",
		lockHelperPath+"="+path,
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe(): %v", err)
	}
	var stderr strings.Builder
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	line, readErr := bufio.NewReader(stdout).ReadString('\n')
	if readErr != nil || strings.TrimSpace(line) != "locked" {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf(
			"child readiness = %q error=%v stderr=%q",
			line,
			readErr,
			stderr.String(),
		)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatalf("Kill(): %v", err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("killed lock holder exited successfully")
	}

	lock, err := state.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire(after forced exit): %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release(): %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stable lock file missing after release: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("lock mode = %o, want 600", info.Mode().Perm())
	}
}

func TestWriterLockDistinguishesContention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "writer.lock")
	first, err := state.Acquire(path)
	if err != nil {
		t.Fatalf("Acquire(first): %v", err)
	}
	t.Cleanup(func() {
		if err := first.Release(); err != nil {
			t.Errorf("Release(first): %v", err)
		}
	})

	_, err = state.Acquire(path)
	if err == nil {
		t.Fatal("Acquire(second) succeeded")
	}
	if !errors.Is(err, state.ErrLockContended) {
		t.Fatalf("Acquire(second) error = %v, want lock contention", err)
	}
}
