package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

type cliDebugKey struct{}

// cliDebug belongs to one CLI invocation. It never prints environment values,
// command arguments, or subprocess output, which can contain credentials.
// Tool bypasses do not create a logger or read this setting.
type cliDebug struct {
	writer  io.Writer
	started time.Time
}

func withCLIDebug(ctx context.Context, writer io.Writer) context.Context {
	enabled, _ := strconv.ParseBool(os.Getenv("DD_TRACE_DEBUG"))
	if !enabled || writer == nil {
		return ctx
	}
	return context.WithValue(ctx, cliDebugKey{}, &cliDebug{writer: &debugWriter{writer: writer}, started: time.Now()})
}

func debugFromContext(ctx context.Context) *cliDebug {
	logger, _ := ctx.Value(cliDebugKey{}).(*cliDebug)
	return logger
}

func (d *cliDebug) printf(format string, args ...any) {
	if d == nil {
		return
	}
	// One write keeps each diagnostic together when Go also writes stderr.
	now := time.Now()
	line := fmt.Sprintf("%s "+version.BuildLogPrefix+" DEBUG: +%s ", now.Format("2006/01/02 15:04:05"), now.Sub(d.started).Round(time.Microsecond))
	line += fmt.Sprintf(format, args...) + "\n"
	_, _ = io.WriteString(d.writer, line)
}

type debugPhase struct {
	logger  *cliDebug
	name    string
	started time.Time
}

func (d *cliDebug) start(name string) debugPhase {
	if d == nil {
		return debugPhase{}
	}
	d.printf("%s started", name)
	return debugPhase{logger: d, name: name, started: time.Now()}
}

func (p debugPhase) finish(err error) {
	if p.logger == nil {
		return
	}
	status := "ok"
	if err != nil {
		status = "error"
	}
	p.logger.printf("%s finished duration=%s status=%s", p.name, time.Since(p.started).Round(time.Microsecond), status)
}

// debugWriter also wraps Go's stderr in debug mode: a cancellation diagnostic
// can arrive while os/exec is copying subprocess output to a caller's buffer.
type debugWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *debugWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(data)
}
