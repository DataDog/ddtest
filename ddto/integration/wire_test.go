package integration

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// This test-only decoder accepts the wire types used by the SDK. It is not part
// of the instrumentator and keeps the POC's production dependency graph empty.
func decodeMsgpack(data []byte) (any, error) {
	r := bytes.NewReader(data)
	value, err := readValue(r, 0)
	if err == nil && r.Len() != 0 {
		return nil, fmt.Errorf("trailing messagepack data")
	}
	return value, err
}
func readValue(r *bytes.Reader, depth int) (any, error) {
	if depth > 64 {
		return nil, fmt.Errorf("messagepack nesting limit")
	}
	tag, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	readN := func(n uint64) ([]byte, error) {
		if n > uint64(r.Len()) {
			return nil, io.ErrUnexpectedEOF
		}
		b := make([]byte, int(n))
		_, e := io.ReadFull(r, b)
		return b, e
	}
	unsigned := func(n int) (uint64, error) {
		b, e := readN(uint64(n))
		if e != nil {
			return 0, e
		}
		var v uint64
		for _, c := range b {
			v = v<<8 | uint64(c)
		}
		return v, nil
	}
	array := func(n uint64) (any, error) {
		if n > uint64(r.Len()) {
			return nil, io.ErrUnexpectedEOF
		}
		v := make([]any, 0, int(n))
		for i := uint64(0); i < n; i++ {
			x, e := readValue(r, depth+1)
			if e != nil {
				return nil, e
			}
			v = append(v, x)
		}
		return v, nil
	}
	object := func(n uint64) (any, error) {
		if n > uint64(r.Len())/2 {
			return nil, io.ErrUnexpectedEOF
		}
		v := map[string]any{}
		for i := uint64(0); i < n; i++ {
			k, e := readValue(r, depth+1)
			if e != nil {
				return nil, e
			}
			key, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("non-string messagepack key")
			}
			x, e := readValue(r, depth+1)
			if e != nil {
				return nil, e
			}
			if _, ok = v[key]; ok {
				return nil, fmt.Errorf("duplicate key")
			}
			v[key] = x
		}
		return v, nil
	}
	if tag <= 0x7f {
		return uint64(tag), nil
	}
	if tag >= 0xe0 {
		return int64(int8(tag)), nil
	}
	if tag >= 0xa0 && tag <= 0xbf {
		b, e := readN(uint64(tag & 31))
		return string(b), e
	}
	if tag >= 0x90 && tag <= 0x9f {
		return array(uint64(tag & 15))
	}
	if tag >= 0x80 && tag <= 0x8f {
		return object(uint64(tag & 15))
	}
	switch tag {
	case 0xc0:
		return nil, nil
	case 0xc2:
		return false, nil
	case 0xc3:
		return true, nil
	case 0xcc, 0xcd, 0xce, 0xcf:
		return unsigned(1 << uint(tag-0xcc))
	case 0xd0, 0xd1, 0xd2, 0xd3:
		n := 1 << uint(tag-0xd0)
		v, e := unsigned(n)
		if e != nil {
			return nil, e
		}
		shift := 64 - n*8
		return int64(v<<shift) >> shift, nil
	case 0xca:
		v, e := unsigned(4)
		return float64(math.Float32frombits(uint32(v))), e
	case 0xcb:
		b, e := readN(8)
		if e != nil {
			return nil, e
		}
		return math.Float64frombits(binary.BigEndian.Uint64(b)), nil
	case 0xd9, 0xda, 0xdb, 0xc4, 0xc5, 0xc6:
		base := byte(0xd9)
		if tag <= 0xc6 {
			base = 0xc4
		}
		n, e := unsigned(1 << uint(tag-base))
		if e != nil {
			return nil, e
		}
		b, e := readN(n)
		if tag >= 0xd9 {
			return string(b), e
		}
		return b, e
	case 0xdc, 0xdd:
		n, e := unsigned(2 << uint(tag-0xdc))
		if e != nil {
			return nil, e
		}
		return array(n)
	case 0xde, 0xdf:
		n, e := unsigned(2 << uint(tag-0xde))
		if e != nil {
			return nil, e
		}
		return object(n)
	default:
		return nil, fmt.Errorf("unsupported messagepack tag %x", tag)
	}
}
