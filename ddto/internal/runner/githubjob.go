package runner

import (
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/goenv"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/githubjob"
)

// gitHubDiagnosticsDirs are the runner directories that hold Worker logs.
var gitHubDiagnosticsDirs = githubjob.DiagnosticsDirs

// gitHubJobIDWait bounds the wait for the scan once preparation is done. When
// the scan is later, as on a stalled filesystem, each test process scans for
// itself, as without ddto.
var gitHubJobIDWait = time.Second

// handGitHubJobID starts reading the GitHub Actions job ID for Mini's test
// processes, each of which would otherwise scan the runner's diagnostics
// files. The returned function waits for the environment entry to add, if any.
// A numeric JOB_CHECK_RUN_ID takes precedence in every process, so it needs no
// scan; the runtime reads GitHub metadata only when GITHUB_SHA is set.
func handGitHubJobID(runtime Runtime, debug *cliDebug) func() []string {
	if _, github := os.LookupEnv("GITHUB_SHA"); runtime != Mini || !github || githubjob.Numeric(strings.TrimSpace(os.Getenv(githubjob.JobIDEnv))) {
		return func() []string { return nil }
	}
	found := make(chan string, 1)
	dirs := gitHubDiagnosticsDirs
	go func() {
		id, _ := githubjob.FromDiagnostics(dirs(), debug.printf)
		found <- id
	}()
	return sync.OnceValue(func() []string {
		select {
		case id := <-found:
			return []string{goenv.GitHubJobIDHandoff + "=" + id}
		case <-time.After(gitHubJobIDWait):
			debug.printf("github job id scan unfinished; test processes scan instead")
			return nil
		}
	})
}
