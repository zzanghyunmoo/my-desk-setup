//go:build windows

package transport

import "os/exec"

func detachTestProcess(*exec.Cmd) {}
