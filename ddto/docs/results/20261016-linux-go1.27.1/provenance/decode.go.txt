package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/msgp/msgp"
)

// The runtime receiver decodes outside the measured cgroup.
func main() {
	data, err := io.ReadAll(io.LimitReader(os.Stdin, 64<<20))
	if err != nil {
		fail(err)
	}
	value, remaining, err := msgp.ReadIntfBytes(data)
	if err != nil {
		fail(err)
	}
	if len(remaining) != 0 {
		fail(fmt.Errorf("trailing MessagePack bytes: %d", len(remaining)))
	}
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
