//go:build windows

package windows

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func TestSystemDLLAndPerformanceCounter(t *testing.T) {
	dll := NewLazySystemDLL("kernel32.dll")
	counter := dll.NewProc("QueryPerformanceCounter")
	frequency := dll.NewProc("QueryPerformanceFrequency")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Go(func() {
			if err := counter.Find(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var first, last, hz int64
	if ok, _, _ := counter.Call(uintptr(unsafe.Pointer(&first))); ok == 0 {
		t.Fatal("counter failed")
	}
	if ok, _, _ := frequency.Call(uintptr(unsafe.Pointer(&hz))); ok == 0 || hz <= 0 {
		t.Fatal("frequency failed")
	}
	time.Sleep(time.Millisecond)
	if ok, _, _ := counter.Call(uintptr(unsafe.Pointer(&last))); ok == 0 || last <= first {
		t.Fatal("counter did not advance")
	}
	if err := dll.NewProc("DDTestOptDoesNotExist").Find(); err == nil {
		t.Fatal("missing procedure succeeded")
	}
	if err := NewLazySystemDLL("DDTestOptDoesNotExist.dll").Load(); err == nil {
		t.Fatal("missing DLL succeeded")
	}
	if _, err := dll.dll.FindProc("bad\x00name"); !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("invalid procedure name: %v", err)
	}
	// The loaded module must come from Windows' system directory.
	name := make([]uint16, 32768)
	result, _, err := dll.NewProc("GetModuleFileNameW").Call(dll.Handle(), uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)))
	if result == 0 {
		t.Fatal(err)
	}
	path := strings.ToLower(syscall.UTF16ToString(name[:result]))
	windowsPath := make([]uint16, 32768)
	n, _, err := dll.NewProc("GetWindowsDirectoryW").Call(uintptr(unsafe.Pointer(&windowsPath[0])), uintptr(len(windowsPath)))
	if n == 0 {
		t.Fatal(err)
	}
	root := syscall.UTF16ToString(windowsPath[:n])
	allowed := false
	for _, directory := range []string{"System32", "SysWOW64", "SysArm32"} {
		if path == strings.ToLower(filepath.Join(root, directory, "kernel32.dll")) {
			allowed = true
		}
	}
	if !allowed {
		t.Fatalf("DLL loaded outside Windows system directories: %q", path)
	}
}

func TestThreadEnumerationAndErrors(t *testing.T) {
	snapshot, err := CreateToolhelp32Snapshot(TH32CS_SNAPTHREAD, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer CloseHandle(snapshot)
	entry := ThreadEntry32{Size: uint32(unsafe.Sizeof(ThreadEntry32{}))}
	if err := Thread32First(snapshot, &entry); err != nil {
		t.Fatal(err)
	}
	found := false
	for {
		if entry.OwnerProcessID == uint32(os.Getpid()) {
			found = true
		}
		if err := Thread32Next(snapshot, &entry); err != nil {
			if !errors.Is(err, ERROR_NO_MORE_FILES) {
				t.Fatal(err)
			}
			break
		}
	}
	if !found {
		t.Fatal("current process has no enumerated thread")
	}
	if err := AssignProcessToJobObject(0, 0); err == nil {
		t.Fatal("invalid assignment succeeded")
	}
	if err := TerminateJobObject(0, 1); err == nil {
		t.Fatal("invalid termination succeeded")
	}
	if _, err := ResumeThread(0); err == nil {
		t.Fatal("invalid thread resume succeeded")
	}
}

func TestJobObjectContainsSuspendedChild(t *testing.T) {
	job, err := CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer CloseHandle(job)
	limits := JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := SetInformationJobObject(job, JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(executable, "-test.run=^TestJobObjectChild$")
	child.Env = append(os.Environ(), "DD_TESTOPT_JOB_CHILD=1")
	child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: CREATE_SUSPENDED}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Process.Release() }()
	process, err := OpenProcess(PROCESS_SET_QUOTA|PROCESS_TERMINATE, false, uint32(child.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer CloseHandle(process)
	if err := AssignProcessToJobObject(job, process); err != nil {
		t.Fatal(err)
	}
	snapshot, err := CreateToolhelp32Snapshot(TH32CS_SNAPTHREAD, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer CloseHandle(snapshot)
	entry := ThreadEntry32{Size: uint32(unsafe.Sizeof(ThreadEntry32{}))}
	if err := Thread32First(snapshot, &entry); err != nil {
		t.Fatal(err)
	}
	resumed := false
	for {
		if entry.OwnerProcessID == uint32(child.Process.Pid) {
			thread, err := OpenThread(THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if err != nil {
				t.Fatal(err)
			}
			_, err = ResumeThread(thread)
			closeErr := CloseHandle(thread)
			if err != nil {
				t.Fatal(err)
			}
			if closeErr != nil {
				t.Fatal(closeErr)
			}
			resumed = true
		}
		if err := Thread32Next(snapshot, &entry); err != nil {
			if !errors.Is(err, ERROR_NO_MORE_FILES) {
				t.Fatal(err)
			}
			break
		}
	}
	if !resumed {
		t.Fatal("suspended child was not resumed")
	}
	if err := TerminateJobObject(job, 1); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- child.Wait() }()
	select {
	case err := <-waited:
		if err == nil {
			t.Fatal("terminated child reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("job child did not terminate")
	}
}

func TestJobObjectChild(t *testing.T) {
	if os.Getenv("DD_TESTOPT_JOB_CHILD") == "1" {
		time.Sleep(time.Minute)
	}
}
