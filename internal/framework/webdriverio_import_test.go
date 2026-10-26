package framework

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/stretchr/testify/require"
)

type webdriverIOEnvironmentExecutor struct {
	root string
	env  *map[string]string
}

func (e webdriverIOEnvironmentExecutor) CombinedOutput(_ context.Context, _ string, _ []string, env map[string]string) ([]byte, error) {
	*e.env = env
	body, _ := json.Marshal(WebdriverIOConfig{Files: []string{filepath.Join(e.root, "example.js")}})
	return nil, os.WriteFile(env["DDTEST_WDIO_INSPECT"], body, 0600)
}

func (e webdriverIOEnvironmentExecutor) Run(_ context.Context, _ string, _ []string, env map[string]string) error {
	*e.env = env
	return nil
}

// Node resolves --import values as URLs: a Windows drive path such as
// C:\Temp\webdriverio_observer.mjs would be read as the URL scheme "c:".
func requireObserverImportIsFileURL(t *testing.T, options string) {
	t.Helper()
	match := regexp.MustCompile(`--import ("(?:[^"\\]|\\.)*")`).FindStringSubmatch(options)
	require.NotNil(t, match, options)
	value, err := strconv.Unquote(match[1])
	require.NoError(t, err)
	parsed, err := url.Parse(value)
	require.NoError(t, err)
	require.Equal(t, "file", parsed.Scheme, value)
	require.Equal(t, "webdriverio_observer.mjs", filepath.Base(parsed.Path), value)
}

func TestWebdriverIOObserverIsImportedAsAFileURL(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.WriteFile(filepath.Join(root, "example.js"), []byte(""), 0600))
	var env map[string]string
	runner := NewWebdriverIO(&testPlatform{})
	runner.executor = webdriverIOEnvironmentExecutor{root: root, env: &env}

	_, err := runner.DiscoverTestFiles(t.Context(), discovery.TestFileSet{})
	require.NoError(t, err)
	requireObserverImportIsFileURL(t, env["NODE_OPTIONS"])

	require.NoError(t, runner.RunTests(t.Context(), []string{"example.js"}, map[string]string{}))
	requireObserverImportIsFileURL(t, env["NODE_OPTIONS"])
}
