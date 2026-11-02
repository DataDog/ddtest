// Package reseed reseeds the Linux kernel's random number generator.
//
// Machines restored from the same memory snapshot start with the same kernel
// random state, and the kernel only reseeds on its own on a timer, up to a
// minute later. Until then they produce the same "random" values, such as
// UUIDs or trace IDs. Reseeding each machine with its own seed right after the
// restore makes them diverge at once.
package reseed

import (
	"errors"
	"fmt"
	"io"
)

const (
	// MinSeedSize rejects empty or truncated seeds.
	MinSeedSize = 16
	// MaxSeedSize rejects files that are clearly not a seed.
	MaxSeedSize = 4096
)

// ErrUnsupported is returned on systems other than Linux.
var ErrUnsupported = errors.New("reseeding the kernel random number generator is only supported on Linux")

// device is the kernel's random device: writes mix bytes into the entropy
// pool, and reseed makes the kernel derive a new key from the pool.
type device interface {
	io.WriteCloser
	reseed() error
}

var openDevice = openRandomDevice

// Reseed mixes seed into the kernel's entropy pool and makes the kernel reseed
// its random number generator at once. Writing the seed alone isn't enough:
// the kernel keeps using its current key until its next timed reseed. The
// reseed needs root (CAP_SYS_ADMIN).
func Reseed(seed []byte) (err error) {
	if len(seed) < MinSeedSize || len(seed) > MaxSeedSize {
		return fmt.Errorf("seed must be %d to %d bytes, got %d", MinSeedSize, MaxSeedSize, len(seed))
	}
	random, err := openDevice()
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, random.Close())
	}()
	if _, err := random.Write(seed); err != nil {
		return fmt.Errorf("mix the seed into the entropy pool: %w", err)
	}
	if err := random.reseed(); err != nil {
		return fmt.Errorf("reseed the kernel random number generator: %w", err)
	}
	return nil
}
