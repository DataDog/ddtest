package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// readPackages decodes go list output as it arrives instead of retaining a
// second, complete JSON copy. It always drains stdout and waits for the command,
// including after a decoding failure. A command failure takes precedence over
// partial or malformed output, as it did with Cmd.Output.
func readPackages(cmd *exec.Cmd, failureContext string) ([]goPackage, error) {
	var stderr strings.Builder
	cmd.Stderr = &stderr
	// A canceled go list is killed; do not wait indefinitely for descendants
	// that might still hold its output pipes.
	cmd.WaitDelay = 5 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%s: %w\n%s", failureContext, err, stderr.String())
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s: %w\n%s", failureContext, err, stderr.String())
	}
	var packages []goPackage
	decoder := json.NewDecoder(stdout)
	var decodeErr error
	for {
		var p goPackage
		if err := decoder.Decode(&p); err != nil {
			if err != io.EOF {
				decodeErr = err
			}
			break
		}
		packages = append(packages, p)
	}
	if decodeErr != nil {
		// A producer can keep writing after a malformed record. Draining avoids
		// blocking it on a full pipe while we wait for its exit status.
		_, _ = io.Copy(io.Discard, stdout)
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("%s: %w\n%s", failureContext, err, stderr.String())
	}
	if decodeErr != nil {
		return nil, decodeErr
	}
	return packages, nil
}
