//go:build unix

package osinfo

import (
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/xsys/unix"
	"strings"
	"testing"
)

func TestKernelMetadata(t *testing.T) {
	info, err := unix.ReadKernelInfo()
	if err != nil {
		t.Fatal(err)
	}
	if KernelName() != info.Name || KernelVersion() != info.Version || KernelRelease() != strings.SplitN(info.Release, "-", 2)[0] {
		t.Fatal("reported kernel metadata changed")
	}
}
