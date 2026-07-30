//go:build windows

package transport

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestWindowsProcessTreeTerminatesUnattachedSuspendedRoot(t *testing.T) {
	command := exec.Command(
		os.Args[0],
		"-test.run=TestWindowsProcessTreeUnattachedHelper",
	)
	command.Env = []string{
		"MDS_UNATTACHED_HELPER=1",
		"SystemRoot=" + os.Getenv("SystemRoot"),
	}
	tree, err := newProcessTree(command)
	if err != nil {
		t.Fatalf("newProcessTree(): %v", err)
	}
	defer func() {
		_ = tree.Close()
	}()
	if err := command.Start(); err != nil {
		t.Fatalf("Start(): %v", err)
	}

	if err := tree.Terminate(); err != nil {
		t.Fatalf("Terminate(unattached): %v", err)
	}
	waited := make(chan error, 1)
	go func() {
		waited <- command.Wait()
	}()
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("unattached suspended root survived termination")
	}
}

func TestWindowsProcessTreeUnattachedHelper(t *testing.T) {
	if os.Getenv("MDS_UNATTACHED_HELPER") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
}

func TestWindowsProcessTreeTreatsReleasedRootAsTerminated(t *testing.T) {
	command := exec.Command(
		os.Args[0],
		"-test.run=TestWindowsProcessTreeReleasedHelper",
	)
	if err := command.Run(); err != nil {
		t.Fatalf("Run(): %v", err)
	}
	tree := &windowsProcessTree{command: command}
	if err := tree.terminateRootProcess(); err != nil {
		t.Fatalf("terminateRootProcess(released): %v", err)
	}
}

func TestWindowsProcessTreeReleasedHelper(t *testing.T) {}
