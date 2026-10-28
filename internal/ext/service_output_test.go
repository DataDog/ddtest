package ext

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestServiceBufferDrainsOutputWithoutUnboundedRetention(t *testing.T) {
	var buffer serviceBuffer
	data := bytes.Repeat([]byte("x"), serviceOutputLimit+123)
	for range 10 {
		n, err := buffer.Write(data)
		require.NoError(t, err)
		require.Equal(t, len(data), n)
	}
	require.Equal(t, serviceOutputLimit, buffer.Len())
}

func TestServiceLifetimeIsIndependentOfIndividualCommandTimeout(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	executor := &DefaultCommandExecutor{Timeout: 20 * time.Millisecond}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	output, err := executor.ServiceOutput(ctx, t.TempDir(), node, []string{"-e", "setTimeout(()=>console.log('service alive'),150)"}, nil)
	require.NoError(t, err, string(output))
	require.Contains(t, string(output), "service alive")
	ctx, cancel = context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err = executor.ServiceOutput(ctx, t.TempDir(), node, []string{"-e", "setInterval(()=>{},100)"}, nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
