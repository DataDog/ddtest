package integrations

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/utils/net"
	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/log"
)

// Settings that need a Git repository URL and commit SHA, which a run outside
// a Git repository lacks, are a warning that says what is missing; any other
// failure, or an empty response, stays an error.
func TestLogSettingsFetchError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"no repository", fmt.Errorf("civisibility.GetSettings: %w", net.ErrNoRepositoryOrCommit), "WARN: civisibility: no CI Visibility settings without a Git repository URL and commit SHA; run in a Git repository with a commit and a remote, or set DD_GIT_REPOSITORY_URL and DD_GIT_COMMIT_SHA."},
		{"request failure", errors.New("civisibility.GetSettings: 500 Internal Server Error"), "ERROR: civisibility: error getting CI visibility settings: civisibility.GetSettings: 500 Internal Server Error"},
		{"empty response", nil, "ERROR: civisibility: error getting CI visibility settings: empty response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recordLogger := new(log.RecordLogger)
			defer log.UseLogger(recordLogger)()
			logSettingsFetchError(tc.err)
			log.Flush()
			logs := recordLogger.Logs()
			if len(logs) != 1 || !strings.Contains(logs[0], tc.want) {
				t.Fatalf("logs %q, want one with %q", logs, tc.want)
			}
		})
	}
}
