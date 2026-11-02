//go:build !linux

package reseed

func openRandomDevice() (device, error) {
	return nil, ErrUnsupported
}
