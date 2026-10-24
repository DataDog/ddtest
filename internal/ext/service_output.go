package ext

import (
	"bytes"
	"context"
	"os/exec"
)

const serviceOutputLimit = 1 << 20

type serviceBuffer struct{ bytes.Buffer }

func (b *serviceBuffer) Write(data []byte) (int, error) {
	size := len(data)
	if remaining := serviceOutputLimit - b.Len(); remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = b.Buffer.Write(data)
	}
	return size, nil
}

// ServiceOutput drains every output pipe but retains at most 1 MiB. Application
// services are long-lived; their diagnostics must not grow with their lifetime.
// Their lifetime belongs to the configuration context, not the per-test timeout.
func (e *DefaultCommandExecutor) ServiceOutput(ctx context.Context, directory, name string, args []string, env map[string]string) ([]byte, error) {
	// no-dd-sa:go-security/command-injection
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = directory
	applyEnvMap(cmd, env)
	prepareCommand(cmd)
	var output serviceBuffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return output.Bytes(), err
}
