//go:build unix

package unix

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestUtsStringPadding(t *testing.T) {
	if got := utsString([]int8{-1, 0, 65, 0, 0}); got != "\xff\x00A" {
		t.Fatalf("signed bytes changed: %q", got)
	}
	if got := utsString([]byte{'A', 0, 'B', 0}); got != "A\x00B" {
		t.Fatalf("interior NUL changed: %q", got)
	}
}

func TestKernelInfoNative(t *testing.T) {
	info, err := ReadKernelInfo()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ flag, want string }{{"-s", info.Name}, {"-r", info.Release}, {"-v", info.Version}} {
		output, err := exec.Command("uname", tc.flag).Output()
		if err != nil {
			t.Fatal(err)
		}
		want := strings.TrimSuffix(string(output), "\n")
		if runtime.GOOS != "linux" && runtime.GOOS != "aix" && runtime.GOOS != "solaris" && runtime.GOOS != "illumos" {
			want = strings.NewReplacer("\n", " ", "\t", " ").Replace(want)
		}
		if tc.want != want {
			t.Errorf("uname %s: %q, want %q", tc.flag, tc.want, want)
		}
	}
}

func TestSysctlUnameFormattingAndFailures(t *testing.T) {
	for _, goos := range []string{"darwin", "freebsd", "netbsd", "openbsd", "dragonfly"} {
		t.Run(goos, func(t *testing.T) {
			queried := []string{}
			info, err := sysctlTestInfo(func(key string) (string, error) {
				queried = append(queried, key)
				switch key {
				case "kern.ostype":
					return "BSD", nil
				case "kern.osrelease":
					return "14.0-custom", nil
				case "kern.version":
					return "build\nwith\ttabs", nil
				default:
					return "unused", nil
				}
			}, goos)
			if err != nil || info != (KernelInfo{"BSD", "14.0-custom", "build with tabs"}) || len(queried) != 5 {
				t.Fatalf("sysctl metadata: %+v/%v, queries=%v", info, err, queried)
			}
			for _, bad := range []string{"kern.ostype", "kern.hostname", "kern.osrelease", "kern.version", "hw.machine"} {
				sentinel := errors.New("native failure")
				_, err := sysctlTestInfo(func(key string) (string, error) {
					if key == bad {
						return "", sentinel
					}
					return "value", nil
				}, goos)
				if !errors.Is(err, sentinel) {
					t.Fatalf("%s error lost: %v", bad, err)
				}
			}
		})
	}
	for _, goos := range []string{"darwin", "netbsd", "openbsd"} {
		if _, err := sysctlTestInfo(func(string) (string, error) { return strings.Repeat("a", 256), nil }, goos); !errors.Is(err, syscall.ENOMEM) {
			t.Fatalf("%s oversized field accepted: %v", goos, err)
		}
	}
	for _, goos := range []string{"freebsd", "dragonfly"} {
		info, err := sysctlTestInfo(func(string) (string, error) { return strings.Repeat("a", 300), nil }, goos)
		size := 256
		if goos == "dragonfly" {
			size = 31
		}
		if err != nil || len(info.Name) != size || len(info.Release) != size {
			t.Fatalf("%s truncation: %+v/%v", goos, info, err)
		}
		if _, err := sysctlTestInfo(func(string) (string, error) { return "partial", syscall.ENOMEM }, goos); err != nil {
			t.Fatalf("%s ENOMEM changed: %v", goos, err)
		}
	}
	info, err := sysctlTestInfo(func(key string) (string, error) {
		if key == "kern.version" {
			return strings.Repeat("a", 31) + "\n", nil
		}
		return "BSD", nil
	}, "dragonfly")
	if err != nil || info.Version != strings.Repeat("a", 31) {
		t.Fatalf("final Version newline changed: %q/%v", info.Version, err)
	}
}

// Feed the actual fixed buffer, including partial data returned with an error.
func sysctlTestInfo(query func(string) (string, error), goos string) (KernelInfo, error) {
	return kernelInfoFromSysctl(func(key string, buffer []byte) error {
		value, err := query(key)
		copy(buffer, value)
		if err == nil && len(value) >= len(buffer) {
			err = syscall.ENOMEM
		}
		return err
	}, goos)
}

func TestSysctlPartialBufferAndMIB(t *testing.T) {
	for _, goos := range []string{"freebsd", "dragonfly"} {
		info, err := kernelInfoFromSysctl(func(key string, buffer []byte) error {
			if len(buffer) != 256 && len(buffer) != 32 {
				t.Fatalf("unexpected fixed buffer size: %d", len(buffer))
			}
			copy(buffer, "partial")
			return syscall.ENOMEM
		}, goos)
		if err != nil || info != (KernelInfo{"partial", "partial", "partial"}) {
			t.Fatalf("partial bytes lost for %s: %+v/%v", goos, info, err)
		}
	}
	for key, expected := range map[string][2]int32{"kern.ostype": {1, 1}, "kern.hostname": {1, 10}, "kern.osrelease": {1, 2}, "kern.version": {1, 4}, "hw.machine": {6, 1}} {
		got, ok := kernelFieldMIB(key)
		if !ok || got != expected {
			t.Fatalf("MIB for %s: %v/%v", key, got, ok)
		}
	}
	if _, ok := kernelFieldMIB("unknown"); ok {
		t.Fatal("unknown kernel field accepted")
	}
}
