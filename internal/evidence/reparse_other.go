//go:build !windows

package evidence

func isReparsePoint(string) (bool, error) {
	return false, nil
}
