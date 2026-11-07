//go:build windows

package fsutil

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobs maps a started child's pid to the job object that holds it and every
// descendant it starts. A process tree cannot be found from parent links once
// the parent has exited, so taskkill /T alone would miss a tool that outlived
// pig; the job object does not.
var jobs sync.Map

// ConfigureProcessGroup makes the child a process group of its own and starts it
// suspended, so AttachProcessGroup can put it in a job object before it runs
// and before it can start a descendant (the same way PiG holds the shell its
// bash tool starts).
func ConfigureProcessGroup(command *exec.Cmd) {
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_SUSPENDED
}

// AttachProcessGroup is called once after the child started: it puts the child
// in a job object that kills its members when the last handle closes, then
// resumes the child. A child that cannot be resumed is killed and the error is
// returned. A child that could not be put in a job is resumed all the same and
// is then ended by taskkill /T, as PiG does when it has no job for a shell.
func AttachProcessGroup(process *os.Process) error {
	job, jobErr := newKillOnCloseJob(process.Pid)
	if jobErr == nil {
		jobs.Store(process.Pid, job)
	}
	if err := resumeProcess(process.Pid); err != nil {
		if jobErr == nil {
			jobs.Delete(process.Pid)
			_ = windows.CloseHandle(job)
		}
		_ = process.Kill()
		return err
	}
	return nil
}

// TerminateProcessGroup sends a CTRL_BREAK event to the child's process group.
// It is not a graceful stop of pig. Go reports the event as SIGINT, and pig
// v0.7.0 in RPC mode handles only SIGTERM on Windows, so the default action
// ends pig at once and skips its session_shutdown hook. The event also reaches
// the group only when this process and the child share a console; otherwise
// the call fails and the caller goes on to KillProcessGroup. The graceful stop,
// on every platform, is closing pig's stdin, which the driver does first.
func TerminateProcessGroup(process *os.Process) error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(process.Pid))
}

// KillProcessGroup ends the child and every process of its job and returns once
// the job has no member left. The job is the guarantee: every descendant joins
// it and none can break away. taskkill /T runs only when the child has no job
// and has not been waited for, because Wait closes the process handle and
// Windows reuses PIDs, so a taskkill on a reaped pid could end an unrelated
// tree (see killPlan). A child that already exited is not an error. The job
// handle is released: a second call has no job and, once the child was waited
// for, does nothing.
func KillProcessGroup(process *os.Process) error {
	value, hasJob := jobs.LoadAndDelete(process.Pid)
	useJob, useTaskkill := killPlan(hasJob, reaped(process))
	if useTaskkill {
		runTaskkill(process.Pid)
	}
	if !useJob {
		return nil
	}
	job := value.(windows.Handle)
	defer func() { _ = windows.CloseHandle(job) }()
	if err := windows.TerminateJobObject(job, 1); err != nil {
		return err
	}
	return waitJobEmpty(job)
}

// reaped reports whether Wait has already released the process. Go returns
// ErrProcessDone once the process has exited and EINVAL once the handle is
// released; a live process answers EWINDOWS to a signal other than Kill.
func reaped(process *os.Process) bool {
	err := process.Signal(syscall.Signal(0))
	return errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.EINVAL)
}

// runTaskkill ends the tree the parent links still show, through the System32
// taskkill and not whatever is first on PATH. Failures are consumed: the
// process may be gone, and the job object is the guarantee.
func runTaskkill(pid int) {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	command := exec.Command(filepath.Join(root, "System32", "taskkill.exe"), "/F", "/T", "/PID", strconv.Itoa(pid))
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS, HideWindow: true}
	_ = command.Run()
}

// newKillOnCloseJob creates a job object holding pid and sets KILL_ON_JOB_CLOSE,
// so the tree ends if the adapter dies before it kills the group.
func newKillOnCloseJob(pid int) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	// x/sys takes the buffer as a uintptr and the goroutine stack can move before the system call, so the buffer is pinned, which places it on the heap.
	info := &windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE},
	}
	var pinner runtime.Pinner
	pinner.Pin(info)
	defer pinner.Unpin()
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(info)), uint32(unsafe.Sizeof(*info))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	if err := windows.AssignProcessToJobObject(job, handle); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

// resumeProcess resumes the main thread of the suspended process pid, the only
// thread a CREATE_SUSPENDED start creates.
func resumeProcess(pid int) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	resumed := false
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != uint32(pid) {
			continue
		}
		thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if openErr != nil {
			continue
		}
		if _, resumeErr := windows.ResumeThread(thread); resumeErr == nil {
			resumed = true
		}
		_ = windows.CloseHandle(thread)
	}
	if !resumed {
		return errors.New("could not resume the suspended child process")
	}
	return nil
}

// jobAccounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION.
type jobAccounting struct {
	TotalUserTime, TotalKernelTime                     int64
	ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses                uint32
	ActiveProcesses, TotalTerminatedProcesses          uint32
}

// waitJobEmpty polls the job until it has no active process. TerminateJobObject
// ends every member but returns before they have released their handles, and a
// descendant that still held the working directory would be seen by the next turn.
func waitJobEmpty(job windows.Handle) error {
	info := new(jobAccounting)
	var pinner runtime.Pinner
	pinner.Pin(info) // see newKillOnCloseJob
	defer pinner.Unpin()
	for range 200 {
		if err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(info)), uint32(unsafe.Sizeof(*info)), nil); err != nil {
			return err
		}
		if info.ActiveProcesses == 0 {
			return nil
		}
		windows.SleepEx(25, false)
	}
	return errors.New("the job still has a running process after TerminateJobObject")
}
