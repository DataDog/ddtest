package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"syscall"
	"time"
	"unsafe"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/goenv"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/xsys/windows"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

// ExecWithCallerEnvironment restores the caller's Go settings, then runs args
// with the native streams and exit status: a go test -exec program sees what
// native go test gives it, rather than ddto's temporary workspace.
//
// Native go test ends a timed-out program by terminating it, which here ends
// only this wrapper. The program therefore runs in a job that kills it when
// the wrapper's handle closes. Once the program exits, the job no longer kills
// processes it left running: they outlive native go test as well. Console
// interrupts reach the program directly, so the wrapper waits for its status.
func ExecWithCallerEnvironment(args []string) int {
	goenv.Restore()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, version.BuildLogPrefix+" ERROR: missing -exec program")
		return 2
	}
	return runInJob(args, "", false, 2, nil)
}

// ExecCommand runs args unchanged, with the native streams and exit status:
// ddto passes every command but go test through. When args are this
// process's own arguments, the command receives the caller's command line
// after ddto's name as written: cmd.exe, which runs batch files, reads its
// quoting otherwise than Go writes it. The command may start processes
// outside its job, as it could without ddto.
func ExecCommand(args []string) int {
	debug := debugFromContext(withCLIDebug(context.Background(), os.Stderr))
	debug.printf("pass-through command=%q args=%d", args[0], len(args)-1)
	if _, err := exec.LookPath(args[0]); err != nil {
		fmt.Fprintln(os.Stderr, version.BuildLogPrefix+" ERROR:", err)
		return commandStatus(err)
	}
	var cmdline string
	if slices.Equal(args, os.Args[1:]) {
		cmdline = commandLineTail(commandLine())
	}
	debug.printf("pass-through caller_command_line=%t", cmdline != "")
	return runInJob(args, cmdline, true, 126, debug)
}

// commandLine is this process's command line.
func commandLine() string {
	p := syscall.GetCommandLine()
	n := 0
	for *(*uint16)(unsafe.Add(unsafe.Pointer(p), 2*n)) != 0 {
		n++
	}
	return syscall.UTF16ToString(unsafe.Slice(p, n))
}

// runInJob runs args, with cmdline as their command line if set, in a job that
// kills them if this wrapper ends first, and returns their exit status, or
// failed if they do not start. With breakaway, the job lets them, and what
// they leave running, start processes outside it (CREATE_BREAKAWAY_FROM_JOB).
// They get this process's environment and standard streams as they are:
// os/exec would add SYSTEMROOT and open NUL for a missing stream.
func runInJob(args []string, cmdline string, breakaway bool, failed int, debug *cliDebug) int {
	signal.Notify(make(chan os.Signal, 1), os.Interrupt)
	path, err := exec.LookPath(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return failed
	}
	attr := &syscall.ProcAttr{
		Env:   syscall.Environ(),
		Files: []uintptr{stdHandle(os.Stdin), stdHandle(os.Stdout), stdHandle(os.Stderr)},
		Sys:   &syscall.SysProcAttr{CmdLine: cmdline},
	}
	started := time.Now()
	process, job, err := startInJob(path, args, attr, breakaway, debug)
	if err != nil {
		debug.printf("pass-through start path=%q failed error=%q", path, err.Error())
		fmt.Fprintln(os.Stderr, err)
		return failed
	}
	debug.printf("pass-through started path=%q job=%t breakaway=%t", path, job != 0, breakaway && job != 0)
	defer syscall.CloseHandle(process)
	code, err := waitProcess(process)
	if job != 0 {
		releaseJob(job, breakaway)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	debug.printf("pass-through exited code=%d duration=%s", code, time.Since(started).Round(time.Microsecond))
	return int(code)
}

// stdHandle is f's handle for a new process, or 0 for a stream that this
// process does not have. A nil os.File's Fd is INVALID_HANDLE_VALUE, which is
// also this process's pseudo-handle: syscall.StartProcess would duplicate it.
func stdHandle(f *os.File) uintptr {
	if f == nil {
		return 0
	}
	return f.Fd()
}

// waitProcess waits for process to exit and returns its exit status.
func waitProcess(process syscall.Handle) (uint32, error) {
	if _, err := syscall.WaitForSingleObject(process, syscall.INFINITE); err != nil {
		return 0, err
	}
	var code uint32
	err := syscall.GetExitCodeProcess(process, &code)
	return code, err
}

// jobObjectLimitBreakawayOK lets a job's processes start others outside it.
const jobObjectLimitBreakawayOK = 0x00000800

// startInJob starts path suspended, so it runs no code before it belongs to a
// job that kills it when closed, then resumes it. Where no job is available,
// it runs without one. It returns the process's handle.
func startInJob(path string, args []string, attr *syscall.ProcAttr, breakaway bool, debug *cliDebug) (syscall.Handle, windows.Handle, error) {
	start := func() (syscall.Handle, uint32, error) {
		pid, handle, err := syscall.StartProcess(path, args, attr)
		return syscall.Handle(handle), uint32(pid), err
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		debug.printf("pass-through job unavailable step=create error=%q", err.Error())
		process, _, err := start()
		return process, 0, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if breakaway {
		limits.BasicLimitInformation.LimitFlags |= jobObjectLimitBreakawayOK
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		debug.printf("pass-through job unavailable step=limits error=%q", err.Error())
		_ = windows.CloseHandle(job)
		process, _, err := start()
		return process, 0, err
	}
	attr.Sys.CreationFlags |= windows.CREATE_SUSPENDED
	process, pid, err := start()
	if err != nil {
		_ = windows.CloseHandle(job)
		return 0, 0, err
	}
	if err := windows.AssignProcessToJobObject(job, windows.Handle(process)); err != nil {
		debug.printf("pass-through job unavailable step=assign error=%q", err.Error())
		_ = windows.CloseHandle(job)
		job = 0
	}
	if err := resumeProcess(pid); err != nil {
		_ = syscall.TerminateProcess(process, 1)
		_, _ = waitProcess(process)
		_ = syscall.CloseHandle(process)
		if job != 0 {
			_ = windows.CloseHandle(job)
		}
		return 0, 0, err
	}
	return process, job, nil
}

// resumeProcess resumes the threads of a process started suspended.
func resumeProcess(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return err
	}
	resumed := false
	for {
		if entry.OwnerProcessID == pid {
			thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if err != nil {
				return err
			}
			_, resumeErr := windows.ResumeThread(thread)
			if err := errors.Join(resumeErr, windows.CloseHandle(thread)); err != nil {
				return err
			}
			resumed = true
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				return err
			}
			break
		}
	}
	if !resumed {
		return errors.New("exec program has no thread to resume")
	}
	return nil
}

// releaseJob closes the job without killing what the program left running,
// which keeps breaking away if the job allowed it.
func releaseJob(job windows.Handle, breakaway bool) {
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	if breakaway {
		limits.BasicLimitInformation.LimitFlags = jobObjectLimitBreakawayOK
	}
	_, _ = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)))
	_ = windows.CloseHandle(job)
}

// Windows has no exec replacement. Preserve streams and native exit status.
func ExecNativeTool(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, version.BuildLogPrefix+" ERROR: missing tool executable")
		return 2
	}
	args, err := chainUserToolexec(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	return 0
}
