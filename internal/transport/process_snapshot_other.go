//go:build !darwin && !linux && !windows

package transport

func descendantProcessIDs(int) []int {
	return nil
}
