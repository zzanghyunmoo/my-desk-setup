package target

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

// ObserveLocal enriches stable target identity with mutable facts used by the
// plan preimage. It is read-only and does not inspect credentials.
func ObserveLocal(
	ctx context.Context,
	facts Facts,
	port transport.Port,
	osReleasePath string,
) (Facts, error) {
	if port == nil {
		return Facts{}, errors.New("target observation transport is required")
	}
	switch facts.OS {
	case "linux":
		if osReleasePath == "" {
			osReleasePath = "/etc/os-release"
		}
		content, err := os.ReadFile(osReleasePath)
		if err != nil {
			return Facts{}, fmt.Errorf("read Linux release identity: %w", err)
		}
		release := parseOSRelease(string(content))
		if release["ID"] != "ubuntu" || release["VERSION_ID"] != "26.04" {
			return Facts{}, fmt.Errorf(
				"v1 requires Ubuntu 26.04, observed %s %s",
				release["ID"],
				release["VERSION_ID"],
			)
		}
		facts.OSVersion = release["VERSION_ID"]
		runtimeResult, err := port.Run(ctx, transport.Command{
			Executable: "uname",
			Arguments:  []string{"-r"},
		})
		if err != nil {
			return Facts{}, fmt.Errorf("observe Linux runtime version: %w", err)
		}
		facts.RuntimeVersion = strings.TrimSpace(runtimeResult.Stdout)
		if facts.RuntimeVersion == "" {
			return Facts{}, errors.New("observe Linux runtime version: empty output")
		}
		imageSum := sha256.Sum256(content)
		facts.ImageRevision = "sha256:" + hex.EncodeToString(imageSum[:])
		if _, err := port.Run(ctx, transport.Command{
			Executable: "systemctl",
			Arguments:  []string{"--version"},
		}); err == nil {
			facts.SystemdSupported = true
		} else if !commandUnavailable(err) {
			return Facts{}, fmt.Errorf("inspect systemd support: %w", err)
		}
		if facts.SystemdSupported {
			result, err := port.Run(ctx, transport.Command{
				Executable: "systemctl",
				Arguments:  []string{"is-system-running"},
			})
			state := strings.TrimSpace(result.Stdout + "\n" + result.Stderr)
			facts.SystemdActive = err == nil ||
				strings.Contains(state, "running") ||
				strings.Contains(state, "degraded")
		}
	case "darwin":
		result, err := port.Run(ctx, transport.Command{
			Executable: "sw_vers",
			Arguments:  []string{"-productVersion"},
		})
		if err != nil {
			return Facts{}, fmt.Errorf("observe macOS version: %w", err)
		}
		facts.OSVersion = strings.TrimSpace(result.Stdout)
	case "windows":
		version, err := observeWindowsVersion()
		if err != nil {
			return Facts{}, fmt.Errorf("observe Windows version: %w", err)
		}
		facts.OSVersion = version
	default:
		return Facts{}, fmt.Errorf("cannot observe unsupported OS %q", facts.OS)
	}
	facts.Reachable = true
	return facts, nil
}

func parseOSRelease(content string) map[string]string {
	values := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		values[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return values
}

func commandUnavailable(err error) bool {
	return errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist)
}
