package compatibility

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
)

// Compare the static compiler and native adapter on the same configs, without
// manual globs. Explicit globs and forced native discovery are checked too.
func TestJestConfigurationParityIntegration(t *testing.T) {
	modules := requireEnv(t, "DDTEST_JEST_NODE_MODULES")
	cases := []struct {
		name, configName, config, include, exclude string
		extra                                      map[string]string
		files, want                                []string
	}{
		{
			name: "async config with preset and environment", configName: "jest.config.cjs",
			config: `module.exports = async () => ({
     preset: './preset/jest-preset.js',
     roots: [process.env.DDTEST_JEST_ROOT],
     testRegex: '/checks/.*\\.check\\.js$',
     testPathIgnorePatterns: ['/fixtures/'],
   })`,
			extra:   map[string]string{"preset/jest-preset.js": `module.exports = {setupFilesAfterEnv: ['<rootDir>/setup.js']}`},
			include: "checks/**/*.check.js", exclude: "checks/fixtures/**",
			files: []string{"checks/a.check.js", "checks/nested/space name.check.js", "checks/fixtures/ignored.check.js", "outside/a.check.js"},
			want:  []string{"checks/a.check.js", "checks/nested/space name.check.js"},
		},
		{
			name: "ESM config with ordered glob negation", configName: "jest.config.mjs",
			config: `export default {
     setupFilesAfterEnv: ['<rootDir>/setup.js'],
     testMatch: ['<rootDir>/checks/**/*.test.js', '!**/ignored/**', '**/ignored/keep.test.js'],
   }`,
			include: "{checks/*.test.js,checks/ignored/keep.test.js}",
			files:   []string{"checks/a.test.js", "checks/ignored/drop.test.js", "checks/ignored/keep.test.js"},
			want:    []string{"checks/a.test.js", "checks/ignored/keep.test.js"},
		},
		{
			name: "projects with nested roots and regex", configName: "jest.config.js",
			config: `module.exports = { projects: [
     { displayName: 'api', rootDir: './packages/api', testRegex: '.*\\.check\\.js$', setupFilesAfterEnv: ['<rootDir>/../../setup.js'] },
     { displayName: 'web', rootDir: './packages/web', testMatch: ['<rootDir>/**/*.spec.js'], setupFilesAfterEnv: ['<rootDir>/../../setup.js'] },
   ] }`,
			include: "{packages/api/**/*.check.js,packages/web/**/*.spec.js}",
			files:   []string{"packages/api/a.check.js", "packages/web/b.spec.js", "packages/web/ignored.test.js"},
			want:    []string{"packages/api/a.check.js", "packages/web/b.spec.js"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetSettingsAfterTest(t)
			root := t.TempDir()
			root, err := filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			t.Chdir(root)
			if err := os.Symlink(modules, "node_modules"); err != nil {
				t.Fatal(err)
			}
			t.Setenv("DDTEST_JEST_ROOT", "<rootDir>/checks")
			writeFixture(t, root, tc.configName, tc.config)
			writeFixture(t, root, "setup.js", "globalThis.configWasLoaded = true\n")
			for file, contents := range tc.extra {
				writeFixture(t, root, file, contents)
			}
			for _, file := range tc.files {
				writeFixture(t, root, file, `test('preserves configuration', () => { expect(globalThis.configWasLoaded).toBe(true) })`)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			binary := filepath.Join(root, "node_modules/.bin/jest")
			output, err := exec.CommandContext(ctx, binary, "--config", tc.configName, "--listTests", "--json", "--runInBand").CombinedOutput()
			if err != nil {
				t.Fatalf("native discovery failed: %v\n%s", err, output)
			}
			var native []string
			if err := json.Unmarshal(output, &native); err != nil {
				t.Fatal(err)
			}
			for i, file := range native {
				native[i], err = filepath.Rel(root, file)
				if err != nil {
					t.Fatal(err)
				}
			}
			requireFiles(t, native, tc.want)
			resultPath := filepath.Join(t.TempDir(), "result.json")
			configureFramework(shellCommand(binary, "--config", tc.configName, "--runInBand"), "")
			staticJest := framework.NewJest()
			fast, err := staticJest.DiscoverTestFilesFast(ctx, discovery.TestFileSet{})
			if err != nil {
				t.Fatalf("static config analysis failed: %v", err)
			}
			requireFiles(t, fast, native)
			listed, err := staticJest.DiscoverTestFilesNative(ctx, discovery.TestFileSet{})
			if err != nil {
				t.Fatal(err)
			}
			requireFiles(t, listed, fast)
			viper.Set("force_full_test_discovery", true)
			settings.Init()
			forced := framework.NewJest()
			listed, err = forced.DiscoverTestFiles(ctx, discovery.TestFileSet{})
			if err != nil {
				t.Fatal(err)
			}
			if !forced.NativeTestFileDiscoveryUsed() {
				t.Fatal("force flag did not select native discovery")
			}
			requireFiles(t, listed, fast)
			viper.Set("force_full_test_discovery", false)
			configureFramework(shellCommand(binary, "--config", tc.configName, "--runInBand", "--json", "--outputFile", resultPath), tc.include)
			viper.Set("tests_exclude_pattern", tc.exclude)
			settings.Init()
			jest := framework.NewJest()
			resolved, err := discovery.ResolveTestFiles(jest.TestPattern(), settings.GetTestsExcludePattern())
			if err != nil {
				t.Fatal(err)
			}
			got, err := jest.DiscoverTestFiles(ctx, resolved)
			if err != nil {
				t.Fatal(err)
			}
			requireFiles(t, got, native)
			// Execute one assigned suite; JSON output detects accidental extra suites.
			if err := jest.RunTests(ctx, []string{tc.want[0]}, nil); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(resultPath)
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Success     bool `json:"success"`
				TestResults []struct {
					Name string `json:"name"`
				} `json:"testResults"`
			}
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			executed := []string{}
			for _, suite := range result.TestResults {
				file, err := filepath.Rel(root, suite.Name)
				if err != nil {
					t.Fatal(err)
				}
				executed = append(executed, file)
			}
			if !result.Success || !slices.Equal(executed, []string{tc.want[0]}) {
				t.Fatalf("execution = %+v", result)
			}
		})
	}
}
