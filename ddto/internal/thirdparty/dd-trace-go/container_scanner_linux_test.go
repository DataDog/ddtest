//go:build linux

package internal

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func TestContainerReadersHandleReadErrors(t *testing.T) {
	failure := errors.New("cgroup read failed")
	if id := parseContainerID(iotest.ErrReader(failure)); id != "" {
		t.Fatalf("failed read supplied a container ID: %q", id)
	}
	if paths := parseCgroupNodePath(iotest.ErrReader(failure)); len(paths) != 0 {
		t.Fatalf("failed read supplied controller paths: %v", paths)
	}
	// Available controller metadata remains useful even if a later read fails.
	input := io.MultiReader(strings.NewReader("0::/pod\n"), iotest.ErrReader(failure))
	if paths := parseCgroupNodePath(input); len(paths) != 1 || paths[""] != "/pod" {
		t.Fatalf("partial metadata changed: %v", paths)
	}
}

func TestContainerReaderStopsAtFirstID(t *testing.T) {
	id := strings.Repeat("a", 64)
	input := io.MultiReader(strings.NewReader("0::/docker/"+id+"\n"), iotest.ErrReader(errors.New("later read failed")))
	if got := parseContainerID(input); got != id {
		t.Fatalf("first ID changed: %q", got)
	}
}
