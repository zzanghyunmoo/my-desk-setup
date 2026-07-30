//go:build !windows

package transport

import (
	"os/exec"
	"syscall"
)

func detachTestProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
