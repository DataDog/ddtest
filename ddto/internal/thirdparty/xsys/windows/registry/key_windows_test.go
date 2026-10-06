//go:build windows

package registry

import (
	"errors"
	"syscall"
	"testing"
)

func TestWindowsVersionReadOnly(t *testing.T) {
	key, err := OpenKey(LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	if value, _, err := key.GetStringValue("EditionID"); err != nil || value == "" {
		t.Fatalf("EditionID %q/%v", value, err)
	}
	if value, _, err := key.GetIntegerValue("CurrentMajorVersionNumber"); err != nil || value == 0 {
		t.Fatalf("major version %d/%v", value, err)
	}
	if _, _, err := key.GetStringValue("DDTestOptValueDoesNotExist"); !errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		t.Fatalf("missing value changed: %v", err)
	}
	if _, _, err := key.GetIntegerValue("EditionID"); !errors.Is(err, ErrUnexpectedType) {
		t.Fatalf("unexpected type changed: %v", err)
	}
	if _, err := OpenKey(LOCAL_MACHINE, "bad\x00key", QUERY_VALUE); !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("invalid key changed: %v", err)
	}
}
