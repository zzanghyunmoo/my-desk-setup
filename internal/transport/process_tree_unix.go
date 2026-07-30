//go:build !windows

package transport

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

type unixProcessTree struct {
	command *exec.Cmd
}

func newProcessTree(command *exec.Cmd) (*unixProcessTree, error) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &unixProcessTree{command: command}, nil
}

func (tree *unixProcessTree) Attach() error {
	return nil
}

func (tree *unixProcessTree) Terminate() error {
	if tree == nil || tree.command == nil || tree.command.Process == nil {
		return nil
	}
	root := tree.command.Process.Pid
	var terminationErrors []error
	// A Unix process group contains reviewed foreground commands and their
	// ordinary children. The snapshot also catches descendants that called
	// setsid but remain discoverable through the parent chain. A double-forked,
	// reparented daemon is intentionally outside this contract; adapters must
	// not launch daemonizing workloads through Executor.
	for _, pid := range descendantProcessIDs(root) {
		if err := killProcess(pid); err != nil {
			terminationErrors = append(terminationErrors, err)
		}
	}
	if err := syscall.Kill(-root, syscall.SIGKILL); err != nil &&
		!errors.Is(err, syscall.ESRCH) &&
		!errors.Is(err, os.ErrProcessDone) {
		terminationErrors = append(terminationErrors, err)
	}
	if err := killProcess(root); err != nil {
		terminationErrors = append(terminationErrors, err)
	}
	return errors.Join(terminationErrors...)
}

func (tree *unixProcessTree) Close() error {
	return nil
}

func killProcess(pid int) error {
	err := syscall.Kill(pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func collectDescendantProcessIDs(
	root int,
	parents map[int]int,
) []int {
	known := map[int]bool{root: true}
	var descendants []int
	for {
		added := false
		for pid, parent := range parents {
			if known[pid] || !known[parent] {
				continue
			}
			known[pid] = true
			descendants = append(descendants, pid)
			added = true
		}
		if !added {
			return descendants
		}
	}
}
