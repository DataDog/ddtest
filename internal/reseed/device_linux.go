package reseed

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

const randomDevicePath = "/dev/urandom"

type randomDevice struct {
	*os.File
}

func openRandomDevice() (device, error) {
	file, err := os.OpenFile(randomDevicePath, os.O_WRONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", randomDevicePath, err)
	}
	return randomDevice{file}, nil
}

func (d randomDevice) reseed() error {
	err := unix.IoctlSetInt(int(d.Fd()), unix.RNDRESEEDCRNG, 0)
	switch {
	case errors.Is(err, unix.EPERM):
		return fmt.Errorf("%w: it needs CAP_SYS_ADMIN, usually by running as root", err)
	case errors.Is(err, unix.ENODATA):
		return fmt.Errorf("%w: the kernel random number generator isn't initialized yet", err)
	}
	return err
}
