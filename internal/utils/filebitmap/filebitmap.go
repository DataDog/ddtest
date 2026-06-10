// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package filebitmap

// FileBitmap represents a memory-efficient, modifiable bitmap.
type FileBitmap struct {
	data []byte
}

// NewFileBitmapFromBytes creates a new FileBitmap from a given byte slice without modifying it.
func NewFileBitmapFromBytes(data []byte) *FileBitmap {
	if data == nil {
		panic("bitmap array source is nil")
	}
	return &FileBitmap{data: data}
}

// FromLineCount creates a new FileBitmap that can hold the specified number of lines (bits).
func FromLineCount(lines int) *FileBitmap {
	size := getSize(lines)
	return &FileBitmap{data: make([]byte, size)}
}

// FromActiveRange creates a FileBitmap with enough space for 'toLine' lines,
// and sets all bits in the range [fromLine, toLine] (1-indexed).
func FromActiveRange(fromLine, toLine int) *FileBitmap {
	if fromLine <= 0 || toLine < fromLine {
		panic("Invalid range")
	}
	fb := FromLineCount(toLine)
	for i := fromLine; i <= toLine; i++ {
		fb.Set(i)
	}
	return fb
}

// getSize returns the number of bytes needed for numOfLines bits.
func getSize(numOfLines int) int {
	return (numOfLines + 7) / 8
}

// Set sets the bit at the given line (1-indexed) to 1.
func (fb *FileBitmap) Set(line int) {
	if fb.data == nil {
		return
	}
	idx := line - 1      // adjust for zero-based index
	byteIndex := idx / 8 // each byte holds 8 bits
	if byteIndex >= len(fb.data) {
		panic("line out of range")
	}
	bitMask := byte(128 >> (idx % 8)) // 128 >> (idx mod 8) creates the proper mask
	fb.data[byteIndex] |= bitMask
}

// IntersectsWith returns true if this bitmap has at least one common set bit with the other bitmap.
func (fb *FileBitmap) IntersectsWith(other *FileBitmap) bool {
	minSize := len(fb.data)
	if len(other.data) < minSize {
		minSize = len(other.data)
	}
	for i := 0; i < minSize; i++ {
		if (fb.data[i] & other.data[i]) != 0 {
			return true
		}
	}
	return false
}

// GetBuffer returns the internal byte buffer of the bitmap.
func (fb *FileBitmap) GetBuffer() []byte {
	return fb.data
}
