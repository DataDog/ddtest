package registry

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestStringDecoding(t *testing.T) {
	for _, text := range []string{"", "Professional", "Edition 🌍", "%SystemRoot%", strings.Repeat("界", 128)} {
		for _, typ := range []uint32{SZ, EXPAND_SZ} {
			words := utf16.Encode([]rune(text))
			data := make([]byte, 2*(len(words)+1))
			for i, word := range words {
				binary.LittleEndian.PutUint16(data[2*i:], word)
			}
			for _, raw := range [][]byte{data, data[:len(data)-2], append(append([]byte(nil), data...), 0xff)} {
				got, kind, err := decodeString(raw, typ)
				if got != text || kind != typ || err != nil {
					t.Fatalf("decoded %q type %d: %q/%d/%v", text, typ, got, kind, err)
				}
			}
		}
	}
	if got, _, err := decodeString([]byte{65, 0, 0, 0, 66, 0}, SZ); got != "A" || err != nil {
		t.Fatalf("embedded terminator: %q %v", got, err)
	}
	if _, kind, err := decodeString(nil, DWORD); kind != DWORD || !errors.Is(err, ErrUnexpectedType) {
		t.Fatalf("wrong string type: %d %v", kind, err)
	}
	if got, _, err := decodeString([]byte{0x00, 0xd8}, SZ); got != "�" || err != nil {
		t.Fatalf("unpaired surrogate: %q %v", got, err)
	}
}

func TestIntegerDecoding(t *testing.T) {
	for _, value := range []uint64{0, 1, 0xffffffff, 0xffffffffffffffff} {
		data := make([]byte, 8)
		binary.LittleEndian.PutUint64(data, value)
		if got, kind, err := decodeInteger(data, QWORD); got != value || kind != QWORD || err != nil {
			t.Fatalf("QWORD %x: %x/%d/%v", value, got, kind, err)
		}
		if value <= 0xffffffff {
			if got, kind, err := decodeInteger(data[:4], DWORD); got != value || kind != DWORD || err != nil {
				t.Fatalf("DWORD %x: %x/%d/%v", value, got, kind, err)
			}
		}
	}
	for _, typ := range []uint32{DWORD, QWORD} {
		for _, size := range []int{0, 1, 3, 5, 7, 9} {
			if _, kind, err := decodeInteger(make([]byte, size), typ); kind != typ || err == nil {
				t.Fatalf("invalid size %d type %d: %d/%v", size, typ, kind, err)
			}
		}
	}
	if _, kind, err := decodeInteger(nil, SZ); kind != SZ || !errors.Is(err, ErrUnexpectedType) {
		t.Fatalf("wrong integer type: %d/%v", kind, err)
	}
}

func TestReadValueGrowthAndConcurrentResize(t *testing.T) {
	want := bytes.Repeat([]byte{42}, 130)
	calls := 0
	data, typ, err := readValue(make([]byte, 8), func(buffer []byte) (uint32, uint32, error) {
		calls++
		if len(buffer) < 64 {
			return 64, SZ, errorMoreData
		}
		if len(buffer) < len(want) {
			return uint32(len(want)), SZ, errorMoreData
		}
		copy(buffer, want)
		return uint32(len(want)), SZ, nil
	})
	if !bytes.Equal(data, want) || typ != SZ || err != nil || calls != 3 {
		t.Fatalf("growth: %d bytes/%d/%v in %d calls", len(data), typ, err, calls)
	}
	for _, returned := range []uint32{0, 8} {
		calls = 0
		_, typ, err := readValue(make([]byte, 8), func([]byte) (uint32, uint32, error) { calls++; return returned, SZ, errorMoreData })
		if err != errorMoreData || typ != 0 || calls != 1 {
			t.Fatalf("non-growing resize was not bounded: %v/%d/%d", err, typ, calls)
		}
	}
	denied := errors.New("access denied")
	if _, typ, err := readValue(make([]byte, 8), func([]byte) (uint32, uint32, error) { return 0, SZ, denied }); err != denied || typ != 0 {
		t.Fatalf("native error changed: %v/%d", err, typ)
	}
	if data, typ, err := readValue(make([]byte, 8), func([]byte) (uint32, uint32, error) { return 0, SZ, nil }); len(data) != 0 || typ != SZ || err != nil {
		t.Fatalf("empty value: %v/%d/%v", data, typ, err)
	}
}
