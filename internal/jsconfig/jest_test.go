package jsconfig

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writeFixture(t testing.TB, dir, name, contents string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestJestStaticConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, file, config string
		extra              map[string]string
		args               []string
		want               []string
	}{
		{name: "default", want: []string{"checks/a.test.js", "checks/ignored/keep.test.js", "checks/ignored/drop.test.js", "packages/web/a.spec.ts", "src/__tests__/plain.js"}},
		{name: "package JSON", file: "package.json", config: `{"jest":{"roots":["checks"],"testRegex":"a\\.test\\.js$"}}`, want: []string{"checks/a.test.js"}},
		{name: "CJS spread and exclusion", file: "jest.config.cjs", config: `const shared = {roots: ['checks']}; module.exports = {...shared, testMatch: ['**/*.test.js','!**/ignored/**','**/ignored/keep.test.js']}`, want: []string{"checks/a.test.js", "checks/ignored/keep.test.js"}},
		{name: "ESM import", file: "jest.config.mjs", config: `import shared from './shared.mjs'; export default {...shared, testPathIgnorePatterns: ['/ignored/']}`, extra: map[string]string{"shared.mjs": `export default {roots: ['checks']}`}, want: []string{"checks/a.test.js"}},
		{name: "ESM named import", file: "jest.config.mjs", config: `import { roots } from './shared.mjs'; export default {roots}`, extra: map[string]string{"shared.mjs": `export const roots = ['packages/web']`}, want: []string{"packages/web/a.spec.ts"}},
		{name: "TypeScript", file: "jest.config.ts", config: `import type {Config} from 'jest'; const config: Config = {roots:['packages/web']}; export default config satisfies Config;`, want: []string{"packages/web/a.spec.ts"}},
		{name: "async pure factory", file: "jest.config.js", config: `module.exports = async () => { const root = process.env.TEST_ROOT || 'checks'; return { roots: [root], testRegex: 'a\\.test\\.js$' } }`, want: []string{"checks/a.test.js"}},
		{name: "local factory import", file: "jest.config.cjs", config: `const config = require('./shared.cjs'); module.exports = config('packages/web');`, extra: map[string]string{"shared.cjs": `module.exports = root => ({roots:[root]})`}, want: []string{"packages/web/a.spec.ts"}},
		{name: "path builtins", file: "jest.config.cjs", config: `const {join} = require('node:path'); const root = join(__dirname, 'packages/web'); module.exports = {roots: [root]}`, want: []string{"packages/web/a.spec.ts"}},
		{name: "conditional", file: "jest.config.cjs", config: `module.exports = () => { if (process.env.TEST_ROOT === 'web') { return {roots: ['packages/web']} } return {roots:['checks'], testPathIgnorePatterns:['/ignored/']} }`, want: []string{"checks/a.test.js"}},
		{name: "preset", file: "jest.config.js", config: `module.exports = {preset:'./preset/jest-preset.js', testPathIgnorePatterns:['/ignored/']}`, extra: map[string]string{"preset/jest-preset.js": `module.exports={roots:['checks']}`}, want: []string{"checks/a.test.js"}},
		{name: "project objects", file: "jest.config.js", config: `module.exports = {projects:[{rootDir:'checks',testRegex:'a\\.test\\.js$'},{rootDir:'packages/web'}]}`, want: []string{"checks/a.test.js", "packages/web/a.spec.ts"}},
		{name: "project paths", file: "jest.config.js", config: `module.exports = {projects:['packages/*']}`, extra: map[string]string{"packages/web/jest.config.js": `module.exports = {testMatch:['**/*.spec.ts']}`}, want: []string{"packages/web/a.spec.ts"}},
		{name: "CLI config and root", file: "alternate.cjs", config: `module.exports = {roots:['checks']}`, args: []string{"--config", "alternate.cjs", "--roots", "packages/web", "--runInBand"}, want: []string{"packages/web/a.spec.ts"}},
		{name: "CLI positionals", args: []string{"--runInBand", "checks/a"}, want: []string{"checks/a.test.js"}},
		{name: "all negative globs", file: "jest.config.js", config: `module.exports = {roots:['checks'],testMatch:['!**/ignored/**']}`, want: []string{"checks/a.test.js", "checks/helper.js"}},
		{name: "explicit empty regex and match", file: "jest.config.json", config: `{"roots":["checks"],"testRegex":[],"testMatch":[]}`, want: []string{"checks/a.test.js", "checks/ignored/keep.test.js", "checks/ignored/drop.test.js", "checks/helper.js"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("TEST_ROOT", "")
			for _, name := range []string{"checks/a.test.js", "checks/ignored/keep.test.js", "checks/ignored/drop.test.js", "checks/helper.js", "packages/web/a.spec.ts", "src/__tests__/plain.js", "node_modules/fake/a.test.js", ".git/a.test.js"} {
				writeFixture(t, dir, name, `throw new Error('must never execute test files')`)
			}
			if tc.file != "" {
				writeFixture(t, dir, tc.file, tc.config)
			}
			for file, content := range tc.extra {
				writeFixture(t, dir, file, content)
			}
			plan, err := CompileJest(context.Background(), JestOptions{Directory: dir, Args: tc.args})
			if err != nil {
				t.Fatal(err)
			}
			got, err := plan.Discover(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			want := slices.Clone(tc.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("got %v; want %v", got, want)
			}
		})
	}
}

func TestJestUnresolvedConfigurations(t *testing.T) {
	for _, config := range []string{
		`const config={}; config.testMatch=['**/*.js']; module.exports=config`,
		`const patterns=['**/*.js']; patterns.push('**/*.ts'); module.exports={testMatch:patterns}`,
		`module.exports = require('some-plugin')()`,
		`module.exports = {get roots() {return ['checks']}}`,
		`module.exports = {testMatch: ['**/!(ignored)/*.js']}`,
		`module.exports = {testRegex: '(?<=checks/).*'}`,
		`module.exports = {testSequencer:'./custom.js'}`,
		`module.exports = {haste:{enableSymlinks:true}}`,
		`module.exports = {testMatch:['**/*.js'],testRegex:'.*'}`,
		`module.exports = {roots: unknownValue}`,
		`module.exports = {...readFromNetwork()}`,
		`require('fs').writeFileSync('should-not-exist','bad'); module.exports={}`,
		`function recurse() {return recurse()} module.exports=recurse()`,
		`module.exports = {roots: ['missing']}`,
	} {
		t.Run(config, func(t *testing.T) {
			dir := t.TempDir()
			writeFixture(t, dir, "jest.config.js", config)
			if _, err := CompileJest(context.Background(), JestOptions{Directory: dir}); err == nil {
				t.Fatal("unsupported config was accepted")
			}
			if _, err := os.Stat(filepath.Join(dir, "should-not-exist")); !os.IsNotExist(err) {
				t.Fatal("config executed")
			}
		})
	}
}

func TestJestConfigFreshInputs(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "jest.config.js", `module.exports = {roots:[process.env.TEST_ROOT]}`)
	for _, root := range []string{"first", "second"} {
		writeFixture(t, dir, root+"/a.test.js", "")
		plan, err := CompileJest(context.Background(), JestOptions{Directory: dir, Env: map[string]string{"TEST_ROOT": root}})
		if err != nil {
			t.Fatal(err)
		}
		got, err := plan.Discover(context.Background())
		if err != nil || !slices.Equal(got, []string{root + "/a.test.js"}) {
			t.Fatalf("%v, %v", got, err)
		}
	}
	writeFixture(t, dir, "jest.config.js", `module.exports={roots:[]}`)
	plan, err := CompileJest(context.Background(), JestOptions{Directory: dir})
	if err != nil {
		t.Fatal(err)
	}
	got, err := plan.Discover(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("stale config: %v, %v", got, err)
	}
}

func TestJestAnalysisCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	writeFixture(t, dir, "jest.config.js", `module.exports={}`)
	if _, err := CompileJest(ctx, JestOptions{Directory: dir}); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestJestRejectsCyclicImports(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "jest.config.js", `module.exports=require('./other')`)
	writeFixture(t, dir, "other.js", `module.exports=require('./jest.config')`)
	if _, err := CompileJest(context.Background(), JestOptions{Directory: dir}); err == nil {
		t.Fatal("accepted cyclic imports")
	}
}

func TestJestCLIDoesNotTurnOptionValuesIntoPathFilters(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a.test.js", "")
	plan, err := CompileJest(context.Background(), JestOptions{Directory: dir, Args: []string{"--json", "false", "--coverage", "true"}})
	if err != nil {
		t.Fatal(err)
	}
	files, err := plan.Discover(context.Background())
	if err != nil || !slices.Equal(files, []string{"a.test.js"}) {
		t.Fatalf("%v %v", files, err)
	}
	for _, args := range [][]string{{"--reporters", "default", "custom"}, {"--bail", "2"}, {"--setupFilesAfterEnv", "a.js", "b.js"}, {"--findRelatedTests", "source.js"}} {
		if _, err := CompileJest(context.Background(), JestOptions{Directory: dir, Args: args}); err == nil {
			t.Fatalf("unsupported CLI silently accepted: %v", args)
		}
	}
}

func BenchmarkJestConfigDiscovery(b *testing.B) {
	dir := b.TempDir()
	writeFixture(b, dir, "jest.config.cjs", `const root='packages'; module.exports={roots:[root],testMatch:['**/*.test.js'],testPathIgnorePatterns:['/fixtures/']}`)
	for i := 0; i < 10000; i++ {
		writeFixture(b, dir, fmt.Sprintf("packages/p%d/%d.test.js", i/100, i), "")
	}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		plan, err := CompileJest(context.Background(), JestOptions{Directory: dir})
		if err != nil {
			b.Fatal(err)
		}
		files, err := plan.Discover(context.Background())
		if err != nil || len(files) != 10000 {
			b.Fatalf("%d files: %v", len(files), err)
		}
	}
}
