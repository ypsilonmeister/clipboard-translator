//go:build windows

package platform

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"
)

const (
	createNoWindow   = 0x08000000
	createNewConsole = 0x00000010
)

// HideConsole stops a console program (llama-server.exe) from opening its own
// console window when started by this GUI/tray process. Its output is already
// redirected to server.log, so nothing is lost.
func HideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

const (
	processSetQuota                 = 0x0100
	processTerminate                = 0x0001
	jobObjectLimitKillOnJobClose    = 0x00002000
	jobObjectExtendedLimitInfoClass = 9
)

// jobExtendedLimitInfo mirrors JOBOBJECT_EXTENDED_LIMIT_INFORMATION (with its
// embedded JOBOBJECT_BASIC_LIMIT_INFORMATION and IO_COUNTERS); Go's natural
// alignment reproduces the C layout on both 32- and 64-bit Windows.
type jobExtendedLimitInfo struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
	IoCounters              [6]uint64
	ProcessMemoryLimit      uintptr
	JobMemoryLimit          uintptr
	PeakProcessMemoryUsed   uintptr
	PeakJobMemoryUsed       uintptr
}

var (
	createJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	setInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	assignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")

	jobOnce   sync.Once
	jobHandle syscall.Handle
	jobErr    error
)

// newKillOnCloseJob creates a job object whose processes are all terminated by
// the OS when its last handle closes, including when this process dies.
func newKillOnCloseJob() (syscall.Handle, error) {
	h, _, err := createJobObjectW.Call(0, 0)
	if h == 0 {
		return 0, fmt.Errorf("CreateJobObject: %w", err)
	}
	var info jobExtendedLimitInfo
	info.LimitFlags = jobObjectLimitKillOnJobClose
	r, _, err := setInformationJobObject.Call(h, jobObjectExtendedLimitInfoClass,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	if r == 0 {
		_ = syscall.CloseHandle(syscall.Handle(h))
		return 0, fmt.Errorf("SetInformationJobObject: %w", err)
	}
	return syscall.Handle(h), nil
}

// assignPidToJob puts the process pid into job.
func assignPidToJob(job syscall.Handle, pid int) error {
	ph, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("OpenProcess: %w", err)
	}
	defer syscall.CloseHandle(ph)
	if r, _, err := assignProcessToJobObject.Call(uintptr(job), uintptr(ph)); r == 0 {
		return fmt.Errorf("AssignProcessToJobObject: %w", err)
	}
	return nil
}

// BindToParentLifetime makes the started child die with this process. Normal
// shutdown stops the server itself, but a crash or a kill from Task Manager
// would otherwise leave llama-server running, holding the port and gigabytes of
// memory, so the next launch could not start. The job handle is deliberately
// never closed: it lives (and its processes with it) until this process exits.
// Best effort: failure only costs that protection, so it is logged.
func BindToParentLifetime(cmd *exec.Cmd) {
	jobOnce.Do(func() { jobHandle, jobErr = newKillOnCloseJob() })
	if jobErr != nil {
		log.Printf("child cleanup on exit unavailable: %v", jobErr)
		return
	}
	if err := assignPidToJob(jobHandle, cmd.Process.Pid); err != nil {
		log.Printf("could not tie %s to this process: %v", filepath.Base(cmd.Path), err)
	}
}

// setupCommand builds the PowerShell invocation that runs setup.ps1 and keeps
// the window open (waiting for Enter) only when the script fails, so an error
// can be read.
func setupCommand(dir string) *exec.Cmd {
	ps := setupPowerShell(filepath.Join(dir, "setup.ps1"))
	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", ps)
	cmd.Dir = dir
	cmd.Env = CleanPowerShellEnv(os.Environ())
	return cmd
}

// RunSetup runs setup.ps1 in its own visible console window and waits for it.
func RunSetup(dir string) error {
	cmd := setupCommand(dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewConsole}
	return cmd.Run()
}

var isProcessInJob = kernel32.NewProc("IsProcessInJob")

// ProcessInKillOnCloseJob reports whether process pid was assigned to the job
// object that ends the llama-server children with this process. It exists for
// the tests of the code that starts the server.
func ProcessInKillOnCloseJob(pid int) (bool, error) {
	if jobErr != nil {
		return false, fmt.Errorf("job object unavailable: %w", jobErr)
	}
	ph, err := syscall.OpenProcess(0x1000, false, uint32(pid)) // PROCESS_QUERY_LIMITED_INFORMATION
	if err != nil {
		return false, err
	}
	defer syscall.CloseHandle(ph)
	var in int32
	if r, _, err := isProcessInJob.Call(uintptr(ph), uintptr(jobHandle), uintptr(unsafe.Pointer(&in))); r == 0 {
		return false, fmt.Errorf("IsProcessInJob: %w", err)
	}
	return in != 0, nil
}
