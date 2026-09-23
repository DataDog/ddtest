package compatibility

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
)

// Exercise actual Jest's glob/regex semantics, including files which look
// almost like matches. Neither side executes these deliberately invalid tests.
func TestJestGeneratedDiscoveryParityIntegration(t *testing.T) {
	modules := requireEnv(t, "DDTEST_JEST_NODE_MODULES")
	resetSettingsAfterTest(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	if err := os.Symlink(modules, "node_modules"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"checks/a.test.js", "checks/.hidden.test.js", "checks/a.spec.ts", "checks/a.test.jsx",
		"checks/a.test.mjs", "checks/a.test.cjs", "checks/a.test.mts", "checks/a.test.cts", "checks/plain.js", "checks/plain.json", "checks/foo-test.js",
		"checks/é.test.js",
		"checks/foo.specspec.js", "checks/[a].test.js", "checks/.test.js", "checks/test.js",
		"checks/nested/b.test.js", "checks/ignored/drop.test.js", "checks/ignored/keep.test.js",
		"checks/__tests__/plain.js", "checks/__tests__/plain.json", "outside/extra.test.js",
	} {
		writeFixture(t, root, name, `this is deliberately not valid JavaScript`)
	}
	cases := []map[string]any{
		{},
		{"roots": []string{"checks"}},
		{"testMatch": []string{"**/*.test.js"}},
		{"testMatch": []string{"**/*.test.js", "!**/ignored/**", "**/ignored/keep.test.js"}},
		{"roots": []string{"checks"}, "testMatch": []string{"!**/ignored/**"}},
		{"testMatch": []string{"**/*.{test,spec}.{js,ts}"}},
		{"testMatch": []string{"**/?(*.)+(spec|test).[jt]s?(x)"}},
		{"testMatch": []string{"**/+(a|b).test.js"}},
		{"testMatch": []string{"**/[ab].test.js"}},
		{"testMatch": []string{"**/é.test.js"}},
		{"testRegex": `checks/.*\.test\.js$`},
		{"testRegex": ""},
		{"testRegex": `<rootDir>/checks/.*\.test\.js$`},
		{"testRegex": []string{`\.spec\.ts$`, `nested/.*\.js$`}},
		{"testMatch": []string{}, "testRegex": []string{}, "roots": []string{"checks"}},
		{"testPathIgnorePatterns": []string{"/ignored/", "/outside/"}},
		{"modulePathIgnorePatterns": []string{"/nested/"}},
		{"moduleFileExtensions": []string{"js"}, "testMatch": []string{"**/*.{js,ts}"}},
		{"rootDir": "checks", "testMatch": []string{"<rootDir>/**/*.test.js"}},
		{"roots": []string{"checks", "checks/nested"}},
		{"cacheDirectory": "checks/ignored"},
		{"rootDir": "checks", "projects": []any{map[string]any{"displayName": "a", "testRegex": `a\.test\.js$`}, map[string]any{"displayName": "b", "testRegex": `b\.test\.js$`}}},
		{"roots": []string{"outside"}, "testPathIgnorePatterns": []string{"/checks/"}, "projects": []any{map[string]any{"displayName": "child", "rootDir": "checks"}}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for i, config := range cases {
		t.Run(fmt.Sprintf("case_%02d", i), func(t *testing.T) {
			data, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			writeFixture(t, root, "jest.config.json", string(data))
			configureFramework(shellCommand(filepath.Join(root, "node_modules/.bin/jest"), "--config", "jest.config.json", "--runInBand"), "")
			j := framework.NewJest()
			fast, err := j.DiscoverTestFilesFast(ctx, discovery.TestFileSet{})
			if err != nil {
				t.Fatal(err)
			}
			native, err := j.DiscoverTestFilesNative(ctx, discovery.TestFileSet{})
			if err != nil {
				t.Fatal(err)
			}
			requireFiles(t, fast, native)
		})
	}
}
