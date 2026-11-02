package reseed

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeDevice struct {
	operations []string
	written    []byte
	writeErr   error
	reseedErr  error
}

func (d *fakeDevice) Write(p []byte) (int, error) {
	d.operations = append(d.operations, "write")
	if d.writeErr != nil {
		return 0, d.writeErr
	}
	d.written = append(d.written, p...)
	return len(p), nil
}

func (d *fakeDevice) reseed() error {
	d.operations = append(d.operations, "reseed")
	return d.reseedErr
}

func (d *fakeDevice) Close() error {
	d.operations = append(d.operations, "close")
	return nil
}

func useFakeDevice(t *testing.T, fake *fakeDevice) {
	t.Helper()
	original := openDevice
	openDevice = func() (device, error) { return fake, nil }
	t.Cleanup(func() { openDevice = original })
}

func TestReseedMixesTheSeedInBeforeReseeding(t *testing.T) {
	fake := &fakeDevice{}
	useFakeDevice(t, fake)
	seed := bytes.Repeat([]byte{0xab}, 32)

	require.NoError(t, Reseed(seed))

	assert.Equal(t, []string{"write", "reseed", "close"}, fake.operations)
	assert.Equal(t, seed, fake.written)
}

func TestReseedRejectsSeedsOfTheWrongSize(t *testing.T) {
	for _, size := range []int{0, MinSeedSize - 1, MaxSeedSize + 1} {
		fake := &fakeDevice{}
		useFakeDevice(t, fake)

		err := Reseed(make([]byte, size))

		require.Error(t, err, "size %d", size)
		assert.Contains(t, err.Error(), "seed must be")
		assert.Empty(t, fake.operations, "size %d opened the device", size)
	}
}

func TestReseedDoesNotReseedWhenTheSeedCannotBeWritten(t *testing.T) {
	fake := &fakeDevice{writeErr: errors.New("device full")}
	useFakeDevice(t, fake)

	err := Reseed(make([]byte, MinSeedSize))

	require.ErrorContains(t, err, "device full")
	assert.Equal(t, []string{"write", "close"}, fake.operations)
}

func TestReseedReportsAFailedReseed(t *testing.T) {
	fake := &fakeDevice{reseedErr: errors.New("operation not permitted")}
	useFakeDevice(t, fake)

	err := Reseed(make([]byte, MinSeedSize))

	require.ErrorContains(t, err, "reseed the kernel random number generator: operation not permitted")
	assert.Equal(t, []string{"write", "reseed", "close"}, fake.operations)
}
