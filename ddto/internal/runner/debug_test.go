package runner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

func TestCLIDebugActivation(t *testing.T) {
	for _, value := range []string{"", "false", "0", "invalid", "true", "1", "TRUE"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("DD_TRACE_DEBUG", value)
			var output bytes.Buffer
			parent := context.Background()
			ctx := withCLIDebug(parent, &output)
			logger := debugFromContext(ctx)
			want := value == "true" || value == "1" || value == "TRUE"
			if (logger != nil) != want {
				t.Fatalf("logger enabled=%t, want %t", logger != nil, want)
			}
			if !want && ctx != parent {
				t.Fatal("disabled logging changed context")
			}
			before := time.Now()
			phase := logger.start("fixture")
			phase.finish(nil)
			after := time.Now()
			if got := output.String(); strings.Contains(got, version.BuildLogPrefix+" DEBUG") != want {
				t.Fatalf("output=%q", got)
			}
			if want {
				for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
					fields := strings.Fields(line)
					if len(fields) < 6 || fields[4] != "DEBUG:" {
						t.Fatalf("missing timestamp: %q", line)
					}
					stamp, err := time.ParseInLocation("2006/01/02 15:04:05", fields[0]+" "+fields[1], time.Local)
					if err != nil || stamp.Before(before.Truncate(time.Second)) || stamp.After(after) {
						t.Fatalf("invalid wall-clock timestamp: %q (%v)", line, err)
					}
					if _, err := time.ParseDuration(strings.TrimPrefix(fields[5], "+")); err != nil {
						t.Fatalf("missing elapsed time: %q", line)
					}
				}
			}
		})
	}
	t.Setenv("DD_TRACE_DEBUG", "true")
	if debugFromContext(withCLIDebug(context.Background(), nil)) != nil {
		t.Fatal("nil writer enabled logging")
	}
}

func TestCLIDebugPhasesAndConcurrentStderr(t *testing.T) {
	t.Setenv("DD_TRACE_DEBUG", "true")
	var output bytes.Buffer
	logger := debugFromContext(withCLIDebug(context.Background(), &output))
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Go(func() {
			for i := 0; i < 100; i++ {
				phase := logger.start("fixture")
				_, _ = io.WriteString(logger.writer, "native stderr\n")
				phase.finish(errors.New("private diagnostic must stay out of phase summaries"))
			}
		})
	}
	workers.Wait()
	var starts, finishes, native int
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		switch {
		case strings.HasSuffix(line, "fixture started"):
			starts++
		case strings.Contains(line, "fixture finished duration=") && strings.HasSuffix(line, "status=error"):
			finishes++
		case line == "native stderr":
			native++
		default:
			t.Fatalf("torn or unexpected log line: %q", line)
		}
	}
	if starts != 800 || finishes != 800 || native != 800 {
		t.Fatalf("starts=%d finishes=%d native=%d", starts, finishes, native)
	}
}

func BenchmarkCLIDebugDisabled(b *testing.B) {
	logger := (*cliDebug)(nil)
	b.ReportAllocs()
	for b.Loop() {
		phase := logger.start("prepare")
		phase.finish(nil)
		logger.printf("plan ready")
	}
}

type failingDebugWriter struct{}

func (failingDebugWriter) Write([]byte) (int, error) { return 0, errors.New("log sink closed") }

func TestCLIDebugWriteFailure(t *testing.T) {
	t.Setenv("DD_TRACE_DEBUG", "true")
	if code := RunRuntime(t.Context(), []string{"-p"}, Mini, nil, io.Discard, failingDebugWriter{}); code != 2 {
		t.Fatalf("broken debug writer changed parse-error exit=%d", code)
	}
}
