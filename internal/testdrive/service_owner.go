package testdrive

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

//go:embed scripts/service_launcher.cjs
var serviceLauncher string

// Establish the same live ownership leases used by browser workers before the
// application is launched. A vanished coordinator closes those leases even
// when no Go cancellation or deferred cleanup can execute.
func (t *Testdrive) ownSetupService(env map[string]string, argv []string) (string, []string, func() error, error) {
	node, err := exec.LookPath("node")
	if err != nil {
		return "", nil, nil, err
	}
	directory, err := os.MkdirTemp("", "ddtest-service-")
	if err != nil {
		return "", nil, nil, err
	}
	if err := markSessionStorage(directory, t.ownershipRoot()); err != nil {
		return "", nil, nil, errors.Join(err, os.RemoveAll(directory))
	}
	owner, err := t.ownProcesses(directory, env)
	if err != nil {
		return "", nil, nil, errors.Join(&processSupervisionError{Code: "process_unavailable", Reason: fmt.Sprint(err)}, os.RemoveAll(directory))
	}
	cleanup := func() error { return errors.Join(owner.Close(), os.RemoveAll(directory)) }
	path := filepath.Join(directory, "service_launcher.cjs")
	if err := os.WriteFile(path, []byte(serviceLauncher), 0600); err != nil {
		return "", nil, nil, errors.Join(err, cleanup())
	}
	env["DDTEST_SERVICE_NODE_OPTIONS"] = env["NODE_OPTIONS"]
	env["NODE_OPTIONS"] = ""
	env["DDTEST_PROCESS_GUARD"] = ""
	return node, append([]string{path}, argv...), cleanup, nil
}
