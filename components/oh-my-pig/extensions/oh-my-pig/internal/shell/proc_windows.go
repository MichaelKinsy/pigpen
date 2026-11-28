//go:build windows

package shell

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Windows Job Object support via stdlib syscall lazy DLL calls (no external
// modules): each spawned bash process is assigned to its own Job Object with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE so `&`-backgrounded descendants remain
// tracked and are terminated on timeout, cancel, or session shutdown/reload
// even after bash.exe itself has exited.

var (
	kernel32DLL                 = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW        = kernel32DLL.NewProc("CreateJobObjectW")
	procSetInformationJobObject = kernel32DLL.NewProc("SetInformationJobObject")
	procQueryInformationJobObj  = kernel32DLL.NewProc("QueryInformationJobObject")
	procAssignProcessToJobObj   = kernel32DLL.NewProc("AssignProcessToJobObject")
	procTerminateJobObject      = kernel32DLL.NewProc("TerminateJobObject")

	winJobMu sync.Mutex
	winJobs  = map[int]syscall.Handle{}
)

const (
	jobObjectBasicAccountingInformation = 1
	jobObjectExtendedLimitInformation   = 9
	jobObjectLimitKillOnJobClose        = 0x00002000
	processSetQuota                     = 0x0100
	processTerminate                    = 0x0001
	winJobAssignAccess                  = processSetQuota | processTerminate
	stillActiveExitCode                 = 259
)

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectExtendedLimitInfo struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

type jobObjectBasicAccountingInfo struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

// defaultShellConfig resolves bash on Windows: Git Bash in standard ProgramFiles
// locations, then bash.exe on PATH. Mirrors PiG's internal/shellconfig/default_windows.go.
func defaultShellConfig() (shellPath string, shellArgs []string, err error) {
	var searched []string
	for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if base == "" {
			continue
		}
		p := base + `\Git\bin\bash.exe`
		searched = append(searched, "  "+p)
		if _, statErr := os.Stat(p); statErr == nil {
			return p, []string{"-c"}, nil
		}
	}
	if p, lookErr := exec.LookPath("bash.exe"); lookErr == nil && p != "" {
		return p, []string{"-c"}, nil
	}
	return "", nil, fmt.Errorf(
		"No bash shell found. Options:\n"+
			"  1. Install Git for Windows: https://git-scm.com/download/win\n"+
			"  2. Add your bash to PATH (Cygwin, MSYS2, etc.)\n\n"+
			"Searched Git Bash in:\n%s", strings.Join(searched, "\n"))
}

func isBashShell(shellPath string) bool {
	base := strings.ToLower(filepath.Base(shellPath))
	return base == "bash" || base == "bash.exe" || strings.HasPrefix(base, "bash-")
}

// configureProcessGroup sets CREATE_NEW_PROCESS_GROUP on Windows so the shell
// runs in its own console process group alongside the Job Object attached
// right after Start via attachProcessGroup.
func configureProcessGroup(cmd *exec.Cmd) {
	const createNewProcessGroup = 0x00000200
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup
}

// attachProcessGroup assigns pid to a dedicated Windows Job Object configured
// with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE so all descendants (including `&`
// background children whose parent bash.exe has already exited) stay tracked
// as a unit and are terminated on kill or session disposal.
func attachProcessGroup(pid int) {
	if pid <= 0 {
		return
	}
	jobH, _, _ := procCreateJobObjectW.Call(0, 0)
	if jobH == 0 {
		return
	}
	job := syscall.Handle(jobH)
	var info jobObjectExtendedLimitInfo
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	r1, _, _ := procSetInformationJobObject.Call(
		uintptr(job),
		uintptr(jobObjectExtendedLimitInformation),
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if r1 == 0 {
		_ = syscall.CloseHandle(job)
		return
	}
	procH, err := syscall.OpenProcess(winJobAssignAccess, false, uint32(pid))
	if err != nil {
		_ = syscall.CloseHandle(job)
		return
	}
	r2, _, _ := procAssignProcessToJobObj.Call(uintptr(job), uintptr(procH))
	_ = syscall.CloseHandle(procH)
	if r2 == 0 {
		_ = syscall.CloseHandle(job)
		return
	}
	winJobMu.Lock()
	if prev, ok := winJobs[pid]; ok && prev != 0 {
		_ = syscall.CloseHandle(prev)
	}
	winJobs[pid] = job
	winJobMu.Unlock()
}

func queryJobActiveProcesses(job syscall.Handle) (uint32, bool) {
	var acct jobObjectBasicAccountingInfo
	r1, _, _ := procQueryInformationJobObj.Call(
		uintptr(job),
		uintptr(jobObjectBasicAccountingInformation),
		uintptr(unsafe.Pointer(&acct)),
		unsafe.Sizeof(acct),
		0,
	)
	if r1 == 0 {
		return 0, false
	}
	return acct.ActiveProcesses, true
}

func taskkillExePath() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "taskkill.exe")
}

func killProcessGroupByPID(pgid int, done <-chan struct{}, grace time.Duration) {
	if pgid <= 0 {
		return
	}
	// On Windows there is no POSIX SIGTERM; terminate the job object (or fall
	// back to taskkill /F /T if job assignment was unavailable) and wait up to
	// grace for the process exit to settle.
	killProcessGroupImmediate(pgid)
	if grace > 0 && done != nil {
		select {
		case <-done:
		case <-time.After(grace):
		}
	}
}

func killProcessGroupImmediate(pgid int) {
	if pgid <= 0 {
		return
	}
	winJobMu.Lock()
	job, hasJob := winJobs[pgid]
	if hasJob {
		delete(winJobs, pgid)
	}
	winJobMu.Unlock()
	if hasJob && job != 0 {
		_, _, _ = procTerminateJobObject.Call(uintptr(job), 1)
		_ = syscall.CloseHandle(job)
		return
	}
	cmd := exec.Command(taskkillExePath(), "/F", "/T", "/PID", strconv.Itoa(pgid))
	_ = cmd.Run()
}

// killProcessTreeGraceful terminates the process tree via its Windows Job Object
// (or System32\taskkill.exe /F /T /PID fallback).
func killProcessTreeGraceful(proc *os.Process, done <-chan struct{}, grace time.Duration) {
	if proc == nil || proc.Pid <= 0 {
		return
	}
	killProcessGroupByPID(proc.Pid, done, grace)
}

// killProcessTreeImmediate terminates the process tree immediately.
func killProcessTreeImmediate(proc *os.Process) {
	if proc == nil || proc.Pid <= 0 {
		return
	}
	killProcessGroupImmediate(proc.Pid)
}

// isProcessGroupAlive reports whether any process in the tracked Windows Job
// Object for pgid is still active; if the job has emptied, its handle is closed
// and removed so no kernel handles leak.
func isProcessGroupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	winJobMu.Lock()
	job, hasJob := winJobs[pgid]
	winJobMu.Unlock()
	if hasJob && job != 0 {
		active, ok := queryJobActiveProcesses(job)
		if ok && active > 0 {
			return true
		}
		winJobMu.Lock()
		if cur, still := winJobs[pgid]; still && cur == job {
			delete(winJobs, pgid)
			_ = syscall.CloseHandle(job)
		}
		winJobMu.Unlock()
		return false
	}
	return isProcessAlive(pgid)
}

// processExitCode returns the process exit code on Windows.
func processExitCode(state *os.ProcessState) int {
	if state == nil {
		return -1
	}
	return state.ExitCode()
}

// isProcessAlive reports whether a process with pid is still running on Windows.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActiveExitCode
}

// forceKillPID terminates a single pid on Windows (test cleanup fallback).
func forceKillPID(pid int) {
	if pid <= 0 {
		return
	}
	if p, err := os.FindProcess(pid); err == nil && p != nil {
		_ = p.Kill()
	}
}

// statePipeSupported is false on Windows (exec.Cmd.ExtraFiles is unsupported);
// Windows falls back to an 0600 file inside the per-session 0700 state dir.
const statePipeSupported = false

// attachStatePipe is a no-op on Windows.
func attachStatePipe(_ *exec.Cmd, _ *os.File) {}
