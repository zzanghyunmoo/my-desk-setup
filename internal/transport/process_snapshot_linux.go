//go:build linux

package transport

import (
	"os"
	"strconv"
	"strings"
)

func descendantProcessIDs(root int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	parents := make(map[int]int)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || !entry.IsDir() {
			continue
		}
		content, err := os.ReadFile("/proc/" + entry.Name() + "/status")
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(content), "\n") {
			if !strings.HasPrefix(line, "PPid:") {
				continue
			}
			parent, err := strconv.Atoi(strings.TrimSpace(
				strings.TrimPrefix(line, "PPid:"),
			))
			if err == nil {
				parents[pid] = parent
			}
			break
		}
	}
	return collectDescendantProcessIDs(root, parents)
}
