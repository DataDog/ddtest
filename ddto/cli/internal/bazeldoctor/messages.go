// Package bazeldoctor validates Bazel test outputs as the rules_test_optimization
// doctor, test_optimization_doctor.py, does: the same checks, messages,
// report and exit codes. The rules' Python doctor resolves its arguments and
// runfiles and hands the rest to this package. Where Python would read an
// input in its own way, such as invalid JSON or an operating system error
// whose text it prints, the run stops with a fallback and the Python doctor
// validates the outputs itself, so its messages stay the same.
package bazeldoctor

import (
	"bytes"
	"strings"
)

const prefix = "[dd-test-optimization-doctor] "

// Log levels, as topt_runtime/log_levels.py defines them.
var levels = map[string]int{"DEBUG": 10, "INFO": 20, "WARN": 30, "ERROR": 40}

// failure is the doctor's _fail: the run stops with this message and exit
// status 1.
type failure struct{ message string }

// fallback stops the run without output: the Python doctor validates the
// outputs instead.
type fallback struct{ reason string }

// interrupted stops the run when its context is cancelled.
type interrupted struct{}

// output collects the doctor's messages until the run ends, so that a
// fallback prints nothing.
type output struct {
	level  int
	stdout bytes.Buffer
	stderr bytes.Buffer
	last   string
	// asRuntime is the uploader's use of the doctor's code: a warning
	// becomes the bep_runtime_warning code and info messages are dropped.
	asRuntime bool
	warnings  int
}

func (out *output) enabled(severity string) bool {
	return levels[severity] >= out.level
}

// fail is _fail.
func (out *output) fail(message string) {
	out.last = message
	out.stderr.WriteString(prefix + message + "\n")
	panic(failure{message})
}

// warn is _warn.
func (out *output) warn(message string) {
	if out.asRuntime {
		out.warnings++
		return
	}
	if out.enabled("WARN") {
		out.stderr.WriteString(prefix + "warning: " + message + "\n")
	}
}

// info is _info.
func (out *output) info(message string) {
	if out.enabled("INFO") && !out.asRuntime {
		out.stderr.WriteString(prefix + message + "\n")
	}
}

// summary is _summary, which writes to standard output.
func (out *output) summary(message string) {
	if out.enabled("INFO") {
		out.stdout.WriteString(prefix + message + "\n")
	}
}

// unsupported stops the run with a fallback.
func unsupported(reason string) {
	panic(fallback{reason})
}

// logLevel is resolve_log_level for the doctor's environment. An invalid
// level makes the doctor fail with this message.
func logLevel(environ func(string) string) (int, string) {
	level := strings.ToUpper(strings.TrimSpace(environ("DD_TEST_OPTIMIZATION_LOG_LEVEL")))
	if level != "" {
		if value, ok := levels[level]; ok {
			return value, ""
		}
		return levels["INFO"], "DD_TEST_OPTIMIZATION_LOG_LEVEL must be ERROR, WARN, INFO, or DEBUG"
	}
	switch strings.ToLower(strings.TrimSpace(environ("DD_TEST_OPTIMIZATION_DEBUG"))) {
	case "1", "true", "yes", "on":
		return levels["DEBUG"], ""
	}
	return levels["INFO"], ""
}
