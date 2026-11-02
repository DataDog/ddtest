package reseed

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// The kernel answers RNDRESEEDCRNG with EPERM without CAP_SYS_ADMIN, which
// test runners and containers usually lack even as root, and an unknown
// request with EINVAL. Success or EPERM both show the request reached the
// right ioctl.
func TestReseedOnThisKernel(t *testing.T) {
	err := Reseed(make([]byte, MinSeedSize))
	if err == nil {
		return
	}

	require.ErrorIs(t, err, unix.EPERM)
	assert.Contains(t, err.Error(), "CAP_SYS_ADMIN")
}
