//go:build windows

package transport

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsProcessTree struct {
	command *exec.Cmd
	job     windows.Handle
}

func newProcessTree(command *exec.Cmd) (*windowsProcessTree, error) {
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP |
			windows.CREATE_SUSPENDED,
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	information := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	information.BasicLimitInformation.LimitFlags =
		windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&information)),
		uint32(unsafe.Sizeof(information)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	return &windowsProcessTree{command: command, job: job}, nil
}

func (tree *windowsProcessTree) Attach() error {
	if tree == nil || tree.command == nil || tree.command.Process == nil {
		return errors.New("command process is unavailable")
	}
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(tree.command.Process.Pid),
	)
	if err != nil {
		return err
	}
	defer func() {
		_ = windows.CloseHandle(process)
	}()
	if err := windows.AssignProcessToJobObject(tree.job, process); err != nil {
		return err
	}
	return resumeWindowsProcess(uint32(tree.command.Process.Pid))
}

func (tree *windowsProcessTree) Terminate() error {
	if tree == nil || tree.job == 0 {
		return nil
	}
	active, queryErr := tree.activeProcessCount()
	if queryErr == nil && active == 0 {
		return nil
	}
	jobErr := windows.TerminateJobObject(tree.job, 1)
	if jobErr == nil {
		return nil
	}
	remaining, retryErr := tree.activeProcessCount()
	if retryErr == nil &&
		remaining == 0 &&
		(errors.Is(jobErr, windows.ERROR_INVALID_PARAMETER) ||
			errors.Is(jobErr, os.ErrProcessDone)) {
		return nil
	}
	if tree.command == nil || tree.command.Process == nil ||
		(tree.command.ProcessState != nil && tree.command.ProcessState.Exited()) {
		return errors.Join(queryErr, jobErr, retryErr)
	}
	processErr := tree.command.Process.Kill()
	if errors.Is(processErr, os.ErrProcessDone) {
		processErr = nil
	}
	return errors.Join(queryErr, jobErr, retryErr, processErr)
}

func (tree *windowsProcessTree) Close() error {
	if tree == nil || tree.job == 0 {
		return nil
	}
	err := windows.CloseHandle(tree.job)
	tree.job = 0
	return err
}

func (tree *windowsProcessTree) activeProcessCount() (uint32, error) {
	information := jobObjectBasicAccountingInformation{}
	var returned uint32
	err := windows.QueryInformationJobObject(
		tree.job,
		windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&information)),
		uint32(unsafe.Sizeof(information)),
		&returned,
	)
	if err != nil {
		return 0, err
	}
	return information.ActiveProcesses, nil
}

// jobObjectBasicAccountingInformation mirrors the Windows
// JOBOBJECT_BASIC_ACCOUNTING_INFORMATION layout.
type jobObjectBasicAccountingInformation struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

func resumeWindowsProcess(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(
		windows.TH32CS_SNAPTHREAD,
		0,
	)
	if err != nil {
		return err
	}
	defer func() {
		_ = windows.CloseHandle(snapshot)
	}()
	entry := windows.ThreadEntry32{
		Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{})),
	}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return err
	}
	resumed := false
	for {
		if entry.OwnerProcessID == pid {
			thread, err := windows.OpenThread(
				windows.THREAD_SUSPEND_RESUME,
				false,
				entry.ThreadID,
			)
			if err != nil {
				return err
			}
			_, resumeErr := windows.ResumeThread(thread)
			closeErr := windows.CloseHandle(thread)
			if resumeErr != nil || closeErr != nil {
				return errors.Join(resumeErr, closeErr)
			}
			resumed = true
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				break
			}
			return err
		}
	}
	if !resumed {
		return errors.New("started Windows process has no resumable thread")
	}
	return nil
}
