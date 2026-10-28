// Copyright 2015 The Go Authors. All rights reserved.
// Adapted from golang.org/x/sys v0.47.0; BSD license in ../../LICENSE.

//go:build windows

package registry

import "syscall"

type Key syscall.Handle

const (
	LOCAL_MACHINE = Key(syscall.HKEY_LOCAL_MACHINE)
	QUERY_VALUE   = 0x00001
)

func (k Key) Close() error { return syscall.RegCloseKey(syscall.Handle(k)) }
func OpenKey(k Key, path string, access uint32) (Key, error) {
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var subkey syscall.Handle
	if err := syscall.RegOpenKeyEx(syscall.Handle(k), ptr, 0, access, &subkey); err != nil {
		return 0, err
	}
	return Key(subkey), nil
}

func (k Key) getValue(name string, buffer []byte) ([]byte, uint32, error) {
	ptr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, 0, err
	}
	return readValue(buffer, func(buffer []byte) (uint32, uint32, error) {
		var typ uint32
		n := uint32(len(buffer))
		err := syscall.RegQueryValueEx(syscall.Handle(k), ptr, nil, &typ, &buffer[0], &n)
		return n, typ, err
	})
}
func (k Key) GetStringValue(name string) (string, uint32, error) {
	data, typ, err := k.getValue(name, make([]byte, 64))
	if err != nil {
		return "", typ, err
	}
	return decodeString(data, typ)
}
func (k Key) GetIntegerValue(name string) (uint64, uint32, error) {
	data, typ, err := k.getValue(name, make([]byte, 8))
	if err != nil {
		return 0, typ, err
	}
	return decodeInteger(data, typ)
}
