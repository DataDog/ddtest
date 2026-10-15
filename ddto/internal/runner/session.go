package runner

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/goenv"
	infra "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/bazel"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/envconfig"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/env"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
	"github.com/tonyredondo/dd-ci-testing-poc/propagation"
)

// testSession is the test session that ddto owns for its go test command:
// see utils.CLISession. Its test processes report in it, and ddto sends it
// once go test exits, with go test's status and wall time.
type testSession struct {
	dir     string
	session utils.CLISession
	start   time.Time
	keep    bool
	stderr  io.Writer
}

// startTestSession creates the test session for the command that started at
// start, and returns it with the environment entry that hands it to go test's
// test processes, or nil and why the test processes report their own
// sessions, as without ddto: the Mini runtime with CI Visibility enabled,
// outside Bazel's offline modes, in a command that runs test binaries, as for
// the Git worker; no launcher, which could run them where ddto's directory
// is not; no CODEOWNERS services, which give each package its own service;
// and a runtime whose test processes join the session.
func startTestSession(cli CLIOptions, opts options, plan Plan, dir string, start time.Time, stderr io.Writer) (*testSession, string) {
	switch {
	case cli.Runtime != Mini:
		return nil, "the SDK runtime"
	case cli.SessionPerBinary:
		return nil, "--session-per-binary"
	case opts.compileOnly || opts.printOnly:
		return nil, "the command runs no test binary"
	case plan.testLauncher:
		return nil, "a launcher runs the test binaries"
	case plan.sessionRefusal != "":
		return nil, plan.sessionRefusal
	case env.Get("DD_SERVICE") == "" && infra.BoolEnv(utils.ServiceFromCodeOwnersEnv, false):
		return nil, "CODEOWNERS services give each package its own session"
	}
	if mode, _ := envconfig.FromEnv(); !envconfig.Enabled(mode) {
		return nil, "CI Visibility is not enabled"
	}
	if mode := bazel.CurrentMode(); mode.ManifestEnabled || mode.PayloadFilesEnabled {
		return nil, "Bazel's offline mode"
	}
	identity, err := propagation.New()
	if err != nil {
		return nil, err.Error()
	}
	sessionDir, err := os.MkdirTemp("", "ddto-session-")
	if err != nil {
		return nil, err.Error()
	}
	s := &testSession{
		dir:     sessionDir,
		session: utils.CLISession{ID: identity.SpanID, Command: utils.TestCommand(os.Args), WorkingDirectory: dir},
		start:   start,
		keep:    cli.KeepCIState,
		stderr:  stderr,
	}
	if err := utils.WriteCLISession(sessionDir, s.session); err != nil {
		_ = os.RemoveAll(sessionDir)
		return nil, err.Error()
	}
	return s, ""
}

// handoff returns the environment entry for go test.
func (s *testSession) handoff() string { return goenv.TestSessionHandoff + "=" + s.dir }

// finish sends the session once go test has exited with exitCode, and
// removes its directory, or keeps it with --keep-ci-state.
func (s *testSession) finish(ctx context.Context, exitCode int, goVersion string, coverage *float64, gitState string) {
	if s == nil {
		return
	}
	debug := debugFromContext(ctx)
	finished := time.Now()
	phase := debug.start("test session")
	summary, err := integrations.FinishCLISession(integrations.CLISessionEnd{
		Dir: s.dir, Session: s.session, Start: s.start, Finish: finished, ExitCode: exitCode, GoVersion: goVersion, Coverage: coverage, GitState: gitState,
	})
	phase.finish(err)
	debug.printf("test session sent=%t processes=%d closed=%d crashed=%d recreated=%d exit_code=%d", summary.Sent, summary.Processes, summary.Closed, summary.Crashed, summary.Recreated, exitCode)
	if err != nil {
		fmt.Fprintln(s.stderr, version.BuildLogPrefix+" WARN: the test session was not sent: "+err.Error())
	}
	if s.keep {
		fmt.Fprintln(s.stderr, version.BuildLogPrefix+" INFO: kept the test session in "+s.dir)
		return
	}
	_ = os.RemoveAll(s.dir)
}

// profileCoverage returns the statement coverage, in percent, of the
// coverage profile that go test wrote at path, as go tool cover -func totals
// it: blocks that several test processes report count once, covered if one
// covered them.
func profileCoverage(path string) (float64, bool) {
	file, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer file.Close()
	type block struct {
		statements int64
		covered    bool
	}
	blocks := map[string]block{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "mode:") || line == "" {
			continue
		}
		// file.go:line.column,line.column statements count, where the file
		// name can contain spaces, as Go's own parser reads it.
		rest, countText, ok1 := cutLastSpace(line)
		block, statementsText, ok2 := cutLastSpace(rest)
		statements, err1 := strconv.ParseInt(statementsText, 10, 64)
		count, err2 := strconv.ParseInt(countText, 10, 64)
		if !ok1 || !ok2 || err1 != nil || err2 != nil {
			return 0, false
		}
		b := blocks[block]
		b.statements = statements
		b.covered = b.covered || count > 0
		blocks[block] = b
	}
	if scanner.Err() != nil {
		return 0, false
	}
	var total, covered int64
	for _, b := range blocks {
		total += b.statements
		if b.covered {
			covered += b.statements
		}
	}
	if total == 0 {
		return 0, false
	}
	return float64(covered) * 100 / float64(total), true
}

// cutLastSpace splits s around its last space.
func cutLastSpace(s string) (before, after string, found bool) {
	if i := strings.LastIndexByte(s, ' '); i >= 0 {
		return s[:i], s[i+1:], true
	}
	return s, "", false
}

// coverProfilePath returns where go test writes the -coverprofile of a
// command run in dir: relative to -outputdir, which is relative to dir.
func coverProfilePath(opts options, dir string) string {
	if opts.coverProfile == "" {
		return ""
	}
	if filepath.IsAbs(opts.coverProfile) {
		return opts.coverProfile
	}
	output := opts.outputDir
	if output == "" {
		output = dir
	} else if !filepath.IsAbs(output) {
		output = filepath.Join(dir, output)
	}
	return filepath.Join(output, opts.coverProfile)
}
