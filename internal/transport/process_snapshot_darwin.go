//go:build darwin

package transport

import "golang.org/x/sys/unix"

func descendantProcessIDs(root int) []int {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil
	}
	parents := make(map[int]int, len(processes))
	for _, process := range processes {
		parents[int(process.Proc.P_pid)] = int(process.Eproc.Ppid)
	}
	return collectDescendantProcessIDs(root, parents)
}
