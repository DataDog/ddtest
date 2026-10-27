// Copyright 2009 The Go Authors. All rights reserved.
// Uname formatting adapted from x/sys v0.47.0; BSD license in ../LICENSE.

//go:build unix

package unix

import (
	"errors"
	"strings"
	"syscall"
)

// Query every field that the former Uname implementation queried, preserving
// errors from hostname and machine even though CI emits only three fields.
// Reproduce its fixed buffer bounds, truncation rules and Version whitespace.
func kernelInfoFromSysctl(query func(string, []byte) error, goos string) (KernelInfo, error) {
	width := 256
	if goos == "dragonfly" {
		width = 32
	}
	var info KernelInfo
	for _, key := range []string{"kern.ostype", "kern.hostname", "kern.osrelease", "kern.version", "hw.machine"} {
		buffer := make([]byte, width)
		err := query(key, buffer)
		if err != nil && !(errors.Is(err, syscall.ENOMEM) && (goos == "freebsd" || goos == "dragonfly")) {
			return KernelInfo{}, err
		}
		if goos == "dragonfly" && key != "kern.version" {
			buffer[width-1] = 0
		}
		if key == "kern.version" {
			for i, b := range buffer {
				if b == '\n' || b == '\t' {
					if i == len(buffer)-1 {
						buffer[i] = 0
					} else {
						buffer[i] = ' '
					}
				}
			}
		}
		value := strings.TrimRight(string(buffer), "\x00")
		switch key {
		case "kern.ostype":
			info.Name = value
		case "kern.osrelease":
			info.Release = value
		case "kern.version":
			info.Version = value
		}
	}
	return info, nil
}

// These stable MIB values are identical on the five BSD targets supported here.
// Use the numeric keys from upstream Uname, avoiding name/size discovery calls.
func kernelFieldMIB(key string) ([2]int32, bool) {
	switch key {
	case "kern.ostype":
		return [2]int32{1, 1}, true
	case "kern.hostname":
		return [2]int32{1, 10}, true
	case "kern.osrelease":
		return [2]int32{1, 2}, true
	case "kern.version":
		return [2]int32{1, 4}, true
	case "hw.machine":
		return [2]int32{6, 1}, true
	default:
		return [2]int32{}, false
	}
}
