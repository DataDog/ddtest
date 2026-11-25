package bazeldoctor

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
)

// postLog writes the uploader's messages as its Python logger did:
// "[dd-uploader] LEVEL: message" on stderr at or above its level, with the
// API key redacted. Until commit, it holds the messages, so that a run that
// falls back to the Python uploader prints nothing.
type postLog struct {
	level          int
	stdout, stderr io.Writer
	secrets        []string
	held           bool
	heldOut        bytes.Buffer
	heldErr        bytes.Buffer
}

// pythonLevelNames are the names that Python's logging prints.
var pythonLevelNames = map[string]string{"DEBUG": "DEBUG", "INFO": "INFO", "WARN": "WARNING", "ERROR": "ERROR"}

func newPostLog(level string, stdout, stderr io.Writer, secrets ...string) *postLog {
	threshold, ok := levels[strings.ToUpper(level)]
	if !ok {
		threshold = levels["INFO"]
	}
	var kept []string
	for _, secret := range secrets {
		if secret != "" {
			kept = append(kept, secret)
		}
	}
	sort.Slice(kept, func(a, b int) bool { return len(kept[a]) > len(kept[b]) })
	return &postLog{level: threshold, stdout: stdout, stderr: stderr, secrets: kept, held: true}
}

func (l *postLog) enabled(level string) bool {
	return levels[level] >= l.level
}

func (l *postLog) redact(message string) string {
	for _, secret := range l.secrets {
		message = strings.ReplaceAll(message, secret, "<redacted>")
	}
	return message
}

func (l *postLog) errWriter() io.Writer {
	if l.held {
		return &l.heldErr
	}
	return l.stderr
}

func (l *postLog) outWriter() io.Writer {
	if l.held {
		return &l.heldOut
	}
	return l.stdout
}

func (l *postLog) logf(level, format string, args ...any) {
	if !l.enabled(level) {
		return
	}
	_, _ = fmt.Fprintf(l.errWriter(), "[dd-uploader] %s: %s\n", pythonLevelNames[level], l.redact(fmt.Sprintf(format, args...)))
}

func (l *postLog) debugf(format string, args ...any) { l.logf("DEBUG", format, args...) }
func (l *postLog) infof(format string, args ...any)  { l.logf("INFO", format, args...) }
func (l *postLog) warnf(format string, args ...any)  { l.logf("WARN", format, args...) }
func (l *postLog) errorf(format string, args ...any) { l.logf("ERROR", format, args...) }

// line prints a report line on stdout.
func (l *postLog) line(text string) {
	_, _ = fmt.Fprintln(l.outWriter(), l.redact(text))
}

// relay prints what the doctor's code printed, in its own words.
func (l *postLog) relay(out *output) {
	_, _ = l.outWriter().Write([]byte(l.redact(out.stdout.String())))
	_, _ = l.errWriter().Write([]byte(l.redact(out.stderr.String())))
	out.stdout.Reset()
	out.stderr.Reset()
}

// commit prints the held messages and prints the next ones directly: the
// run can no longer fall back.
func (l *postLog) commit() {
	if !l.held {
		return
	}
	l.held = false
	_, _ = l.stdout.Write(l.heldOut.Bytes())
	_, _ = l.stderr.Write(l.heldErr.Bytes())
	l.heldOut.Reset()
	l.heldErr.Reset()
}
