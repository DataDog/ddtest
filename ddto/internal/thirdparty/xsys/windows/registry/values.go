// Copyright 2015 The Go Authors. All rights reserved.
// Adapted from golang.org/x/sys v0.47.0; BSD license in ../../LICENSE.

// Package registry contains the read-only operations needed for CI metadata.
package registry

import (
	"encoding/binary"
	"errors"
	"syscall"
	"unicode/utf16"
)

const (
	SZ        = 1
	EXPAND_SZ = 2
	DWORD     = 4
	QWORD     = 11
)

var ErrUnexpectedType = errors.New("unexpected key value type")

const errorMoreData = syscall.Errno(234)

// Grow only when the native call reports a strictly larger buffer. Preserve
// the original error when a value changes concurrently without growing.
func readValue(buffer []byte, query func([]byte) (uint32, uint32, error)) ([]byte, uint32, error) {
	for {
		n, typ, err := query(buffer)
		if err == nil {
			return buffer[:n], typ, nil
		}
		if err != errorMoreData || n <= uint32(len(buffer)) {
			return nil, 0, err
		}
		buffer = make([]byte, n)
	}
}

func decodeString(data []byte, typ uint32) (string, uint32, error) {
	if typ != SZ && typ != EXPAND_SZ {
		return "", typ, ErrUnexpectedType
	}
	words := make([]uint16, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		word := binary.LittleEndian.Uint16(data[i:])
		if word == 0 {
			break
		}
		words = append(words, word)
	}
	return string(utf16.Decode(words)), typ, nil
}

func decodeInteger(data []byte, typ uint32) (uint64, uint32, error) {
	switch typ {
	case DWORD:
		if len(data) != 4 {
			return 0, typ, errors.New("DWORD value is not 4 bytes long")
		}
		return uint64(binary.LittleEndian.Uint32(data)), typ, nil
	case QWORD:
		if len(data) != 8 {
			return 0, typ, errors.New("QWORD value is not 8 bytes long")
		}
		return binary.LittleEndian.Uint64(data), typ, nil
	default:
		return 0, typ, ErrUnexpectedType
	}
}
