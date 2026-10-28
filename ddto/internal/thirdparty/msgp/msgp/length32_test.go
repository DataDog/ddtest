package msgp

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"testing"
)

// stream reads b without io.Seeker, so the reader cannot skip past its end.
func stream(b []byte) *Reader { return NewReader(struct{ io.Reader }{bytes.NewReader(b)}) }

// Objects whose 32-bit length an int cannot hold on 32-bit platforms read
// as short bytes, as on 64-bit platforms, since no slice holds them, and
// skipping or copying one from a stream fails. Nothing panics or reads them
// with a wrapped length.
func TestLengthsBeyondInt(t *testing.T) {
	for _, length := range []uint32{1 << 31, 1<<32 - 1} {
		header := func(lead byte) []byte {
			return []byte{lead, byte(length >> 24), byte(length >> 16), byte(length >> 8), byte(length)}
		}
		bin, str, array, object := header(mbin32), header(mstr32), header(marray32), header(mmap32)
		ext := append(header(mext32), 1, 0)
		for name, read := range map[string]func() error{
			"ReadBytesBytes": func() error { _, _, err := ReadBytesBytes(append(bin, 0), nil); return err },
			"ReadBytesZC":    func() error { _, _, err := ReadBytesZC(append(bin, 0)); return err },
			"ReadStringZC":   func() error { _, _, err := ReadStringZC(append(str, 0)); return err },
			"ReadStringBytes": func() error {
				_, _, err := ReadStringBytes(append(str, 0))
				return err
			},
			"ReadExtensionBytes": func() error {
				_, err := ReadExtensionBytes(ext, &RawExtension{Type: 1})
				return err
			},
			"Skip bin":   func() error { _, err := Skip(bin); return err },
			"Skip ext":   func() error { _, err := Skip(ext); return err },
			"Skip array": func() error { _, err := Skip(array); return err },
			"Skip map":   func() error { _, err := Skip(object); return err },
		} {
			if err := read(); !errors.Is(err, ErrShortBytes) {
				t.Errorf("%s of length %d: %v, want ErrShortBytes", name, length, err)
			}
		}
		for _, b := range [][]byte{bin, ext, object} {
			if err := stream(b).Skip(); err == nil {
				t.Errorf("Reader.Skip of %x: no error", b)
			}
			if _, err := stream(b).CopyNext(io.Discard); err == nil {
				t.Errorf("Reader.CopyNext of %x: no error", b)
			}
		}
		// On 64-bit platforms, Raw would allocate the object's size.
		if strconv.IntSize == 32 {
			var raw Raw
			if err := raw.DecodeMsg(stream(bin)); !errors.Is(err, ErrLimitExceeded) {
				t.Errorf("Raw.DecodeMsg of length %d: %v, want ErrLimitExceeded", length, err)
			}
		}
	}
	// Nor does Raw overflow growing its buffer for an object that fits an
	// int: after an array's header, or in a buffer that it reuses.
	if strconv.IntSize == 32 {
		for name, read := range map[string]func() error{
			"array of 2^31-1": func() error { var raw Raw; return raw.DecodeMsg(stream([]byte{0x91, mbin32, 0x7f, 0xff, 0xff, 0xfa})) },
			"array of 2^31-2": func() error { var raw Raw; return raw.DecodeMsg(stream([]byte{0x91, mbin32, 0x7f, 0xff, 0xff, 0xf9})) },
			"reused buffer": func() error {
				raw := make(Raw, 0, 16)
				return raw.DecodeMsg(stream([]byte{mbin32, 0x7f, 0xff, 0xff, 0xe0}))
			},
		} {
			if err := read(); !errors.Is(err, ErrLimitExceeded) {
				t.Errorf("Raw.DecodeMsg of %s: %v, want ErrLimitExceeded", name, err)
			}
		}
		// An object that the buffer holds needs no growth.
		raw := make(Raw, 0, 1<<30)
		if err := raw.DecodeMsg(stream([]byte{0x01})); err != nil || !bytes.Equal(raw, []byte{0x01}) {
			t.Errorf("Raw.DecodeMsg in a 1 GiB buffer: %x, %v", []byte(raw), err)
		}
	}
}

// CopyNext streams an object, or a map's elements, beyond math.MaxInt32 on
// any platform: it copies what the stream holds until its end.
func TestCopyNextStreamsLargeObjects(t *testing.T) {
	for name, b := range map[string][]byte{
		"bin": append([]byte{mbin32, 0x80, 0, 0, 0}, make([]byte, 10)...),
		"map": {mmap32, 0x40, 0, 0, 0, 0x01, 0x02},
	} {
		n, err := stream(b).CopyNext(io.Discard)
		if n != int64(len(b)) || err == nil {
			t.Errorf("%s: copied %d of %d bytes, error %v", name, n, len(b), err)
		}
	}
}
