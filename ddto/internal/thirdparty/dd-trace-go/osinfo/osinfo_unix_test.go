//go:build unix

package osinfo

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/xsys/unix"
)

func TestOSReleaseHandlesMalformedLinesAndReadErrors(t *testing.T) {
	name, version := osName, osVersion
	t.Cleanup(func() { osName, osVersion = name, version })
	osName, osVersion = "fallback", ""
	failure := errors.New("OS release read failed")
	input := io.MultiReader(strings.NewReader("NAME\nNAME=\"Example Linux\"\nVERSION_ID=25\nVERSION=\"25 Stable\"\n"), iotest.ErrReader(failure))
	if err := readOSRelease(input); !errors.Is(err, failure) {
		t.Fatalf("read failure lost: %v", err)
	}
	if osName != "Example Linux" || osVersion != "25 Stable" {
		t.Fatalf("available metadata changed: name=%q version=%q", osName, osVersion)
	}
	if err := readOSRelease(strings.NewReader("NAME\nVERSION\n")); err != nil {
		t.Fatal(err)
	}
	if osName != "Example Linux" || osVersion != "25 Stable" {
		t.Fatal("malformed lines replaced available metadata")
	}
}

func TestKernelMetadata(t *testing.T) {
	info, err := unix.ReadKernelInfo()
	if err != nil {
		t.Fatal(err)
	}
	if KernelName() != info.Name || KernelVersion() != info.Version || KernelRelease() != strings.SplitN(info.Release, "-", 2)[0] {
		t.Fatal("reported kernel metadata changed")
	}
}
