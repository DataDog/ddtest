package testdrive

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

type webdriverIOImportExecutor struct{ env map[string]string }

func (e *webdriverIOImportExecutor) CombinedOutput(_ context.Context, _ string, _ []string, env map[string]string) ([]byte, error) {
	e.env = env
	return nil, os.WriteFile(env["DDTEST_WDIO_INSPECT"], []byte(`{}`), 0600)
}

func (e *webdriverIOImportExecutor) Run(context.Context, string, []string, map[string]string) error {
	return nil
}

// Node resolves --import values as URLs: a Windows drive path such as
// C:\Temp\webdriverio_observer.mjs would be read as the URL scheme "c:".
func TestWebdriverIOPreflightImportsTheObserverAsAFileURL(t *testing.T) {
	drive := preparedTestdrive(t)
	executor := &webdriverIOImportExecutor{}
	drive.executor = executor
	drive.command, drive.args = "npx", []string{"wdio", "run", "wdio.conf.js"}
	_, err := drive.inspectWebdriverIO(t.Context(), t.TempDir())
	require.NoError(t, err)
	match := regexp.MustCompile(`--import ("(?:[^"\\]|\\.)*")`).FindStringSubmatch(executor.env["NODE_OPTIONS"])
	require.NotNil(t, match, executor.env["NODE_OPTIONS"])
	value, err := strconv.Unquote(match[1])
	require.NoError(t, err)
	parsed, err := url.Parse(value)
	require.NoError(t, err)
	require.Equal(t, "file", parsed.Scheme, value)
	require.Equal(t, "webdriverio_observer.mjs", filepath.Base(parsed.Path), value)
}
