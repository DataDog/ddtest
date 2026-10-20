package testdrive

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestProbePublicationExplainsWindowsFilesystemErrors(t *testing.T) {
	for _, cause := range []error{windows.ERROR_NOT_SAME_DEVICE, windows.ERROR_NOT_SUPPORTED, windows.ERROR_CALL_NOT_IMPLEMENTED} {
		err := probePublicationError(&os.LinkError{Op: "link", Old: "C:\\contents", New: "D:\\probe.test.js", Err: cause})
		require.ErrorIs(t, err, errProbePublicationUnsupported)
		require.ErrorIs(t, err, cause)
	}
}
