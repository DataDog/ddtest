// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// coverageEdit is the exact change that makes a recognized application build
// expose Istanbul's window.__coverage__ to Cypress. ddtest never applies it
// itself. It is prescribed: a dev-dependency install plus test-only coverage
// config, gated by an environment variable so other builds stay uninstrumented,
// so the agent applies it as given and the final report lists it with a revert.
type coverageEdit struct {
	Build            string     `json:"build"`
	Install          string     `json:"install"`
	Files            []fileEdit `json:"files"`
	Serve            string     `json:"serve"`
	RequiresApproval bool       `json:"requires_approval"`
}

type fileEdit struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	// Content is the whole file for create, or the exact text to add for edit.
	Content string `json:"content"`
	Where   string `json:"where,omitempty"`
	// Replace, when set, is the exact existing text (ignoring indentation) that
	// Content replaces.
	Replace string `json:"replace,omitempty"`
}

// Babel 8 is the current major, but babelify and babel-plugin-istanbul 7
// require Babel 7: unpinned installs fail to resolve, so every Babel package
// in an edit comes from the 7 line.
const (
	babelCore     = "@babel/core@^7"
	babelIstanbul = "babel-plugin-istanbul@^7"
	babelifyPin   = "babelify@^10"
)

// babelTestCoverage scopes Istanbul to builds run with BABEL_ENV=test, so
// ordinary and production builds stay uninstrumented.
const babelTestCoverage = `{
  "env": {
    "test": {
      "plugins": ["istanbul"]
    }
  }
}
`

var browserifyInvocation = regexp.MustCompile(`(?:^|[\s&;|])((?:node\s+)?(?:\./)?(?:node_modules/\.bin/browserify|node_modules/browserify/bin/cmd(?:\.js)?|browserify))(\s)`)

// cypressCoverageEdit recognizes the application build served to Cypress and
// returns the exact instrumentation change, or nil when the build is not one
// ddtest recognizes; the prose guidance then applies.
func cypressCoverageEdit(root string) *coverageEdit {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	var manifest struct {
		Scripts         map[string]string `json:"scripts"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return nil
	}
	depends := func(name string) bool {
		_, dev := manifest.DevDependencies[name]
		_, prod := manifest.Dependencies[name]
		return dev || prod
	}
	install := func(packages ...string) string {
		switch {
		case coverageFileExists(filepath.Join(root, "pnpm-lock.yaml")):
			return "pnpm add --save-dev " + strings.Join(packages, " ")
		case coverageFileExists(filepath.Join(root, "yarn.lock")):
			return "yarn add --dev " + strings.Join(packages, " ")
		case coverageFileExists(filepath.Join(root, "bun.lock")) || coverageFileExists(filepath.Join(root, "bun.lockb")):
			return "bun add --dev " + strings.Join(packages, " ")
		}
		return "npm install --save-dev " + strings.Join(packages, " ")
	}
	// An existing Babel configuration would replace the one proposed here.
	for _, name := range []string{".babelrc", ".babelrc.json", ".babelrc.js", "babel.config.json", "babel.config.js", "babel.config.cjs", "babel.config.mjs"} {
		if coverageFileExists(filepath.Join(root, name)) {
			return nil
		}
	}
	if config := firstExisting(root, "vite.config.ts", "vite.config.js", "vite.config.mts", "vite.config.mjs", "vite.config.cjs"); config != "" && depends("vite") {
		plugin := viteIstanbulPlugin(installedMajor(root, "vite"))
		if plugin == "" {
			return nil
		}
		return &coverageEdit{
			Build:   "vite",
			Install: install(plugin),
			Files: []fileEdit{{Path: config, Action: "edit", Where: "add the import at the top and the plugin to the plugins array of the exported config",
				Content: "import istanbul from 'vite-plugin-istanbul'\n\nistanbul({ include: 'src/**/*', exclude: ['node_modules', 'cypress/'], extension: ['.js', '.jsx', '.ts', '.tsx', '.vue'], requireEnv: true })\n"}},
			Serve:            "Start the application served to Cypress with VITE_COVERAGE=true; other builds stay uninstrumented.",
			RequiresApproval: false,
		}
	}
	if config := firstExisting(root, "webpack.config.js", "webpack.config.cjs", "webpack.config.mjs", "webpack.config.ts"); config != "" && depends("webpack") {
		return &coverageEdit{
			Build:   "webpack",
			Install: install("babel-loader@^10", babelCore, babelIstanbul),
			Files: []fileEdit{
				{Path: "babel.config.json", Action: "create", Content: babelTestCoverage},
				{Path: config, Action: "edit", Where: "add as the first entry of module.rules",
					Content: "{ test: /\\.[cm]?[jt]sx?$/, exclude: /node_modules/, use: 'babel-loader' },\n"},
			},
			Serve:            "Build the application served to Cypress with BABEL_ENV=test; other builds stay uninstrumented.",
			RequiresApproval: false,
		}
	}
	for _, name := range slices.Sorted(maps.Keys(manifest.Scripts)) {
		script := manifest.Scripts[name]
		match := browserifyInvocation.FindStringSubmatchIndex(script)
		if match == nil {
			continue
		}
		environment := "BABEL_ENV=test "
		if depends("cross-env") {
			environment = "cross-env BABEL_ENV=test "
		}
		coverage := name + ":coverage"
		edit := &coverageEdit{Build: "browserify", RequiresApproval: false}
		if source, entry, ts := typescriptBrowserifySource(root, script, match); ts != nil {
			// tsc compiles to gitignored JavaScript first: instrumenting that output
			// would name untracked files, so the coverage build bundles the tracked
			// TypeScript sources with Babel instead, keeping tsc's module semantics.
			rest := strings.TrimSpace(script[match[3]:][entry.end:])
			instrumented := environment + script[match[2]:match[3]] + " " + source + " --extension .ts --extension .tsx -t [ babelify --extensions .ts,.tsx ]"
			if rest != "" {
				instrumented += " " + rest
			}
			edit.Install = install(ts.packages()...)
			edit.Files = []fileEdit{
				{Path: "babel.config.json", Action: "create", Content: ts.babelConfig()},
				{Path: "package.json", Action: "edit", Where: "add to scripts", Content: fmt.Sprintf("%q: %q\n", coverage, instrumented)},
			}
		} else {
			// Scope the environment to the browserify command itself, which may follow
			// other commands, and add the transform right after its executable.
			instrumented := script[:match[2]] + environment + script[match[2]:match[3]] + " -t babelify" + script[match[3]:]
			edit.Install = install(babelifyPin, babelCore, babelIstanbul)
			edit.Files = []fileEdit{
				{Path: "babel.config.json", Action: "create", Content: babelTestCoverage},
				{Path: "package.json", Action: "edit", Where: "add to scripts", Content: fmt.Sprintf("%q: %q\n", coverage, instrumented)},
			}
		}
		serve, files := coverageServe(root, manifest.Scripts, name, coverage)
		edit.Serve = serve
		edit.Files = append(edit.Files, files...)
		return edit
	}
	return nil
}

var tscProject = regexp.MustCompile(`(?:^|[\s&;|])(?:node\s+)?(?:\./)?(?:node_modules/\.bin/tsc|node_modules/typescript/bin/tsc|tsc)\s[^&;|]*?(?:-p|--project)\s+(\S+)`)

// typescriptBuild describes a tsc-then-browserify build whose coverage build
// bundles the TypeScript sources directly.
type typescriptBuild struct {
	jsx bool
	// noInterop matches tsc's CommonJS output without esModuleInterop, where
	// `import * as x` is a plain require.
	noInterop bool
}

type scriptToken struct{ start, end int }

// typescriptBrowserifySource returns the tracked TypeScript entry for a
// browserify bundle of tsc output: tsc must run earlier in the same script and
// the bundled .js entry must have a .ts/.tsx sibling.
func typescriptBrowserifySource(root, script string, match []int) (string, scriptToken, *typescriptBuild) {
	project := tscProject.FindStringSubmatch(script[:match[2]])
	if project == nil {
		return "", scriptToken{}, nil
	}
	args := script[match[3]:]
	offset := 0
	for _, field := range strings.Fields(args) {
		at := strings.Index(args[offset:], field) + offset
		token := scriptToken{start: at, end: at + len(field)}
		offset = token.end
		if strings.HasPrefix(field, "-") {
			// Options before the entry take no value only when they are flags; stop
			// at the first value-taking option, as the entry is then ambiguous.
			if field == "-o" || field == "--outfile" || field == "-t" || field == "--transform" {
				return "", scriptToken{}, nil
			}
			continue
		}
		if !strings.HasSuffix(field, ".js") {
			return "", scriptToken{}, nil
		}
		base := strings.TrimSuffix(field, ".js")
		for _, extension := range []string{".tsx", ".ts"} {
			if coverageFileExists(filepath.Join(root, filepath.FromSlash(base+extension))) {
				build := &typescriptBuild{jsx: extension == ".tsx", noInterop: true}
				config := filepath.Join(root, filepath.FromSlash(project[1]))
				if info, err := os.Stat(config); err == nil && info.IsDir() {
					config = filepath.Join(config, "tsconfig.json")
				}
				if data, err := os.ReadFile(config); err == nil {
					build.noInterop = !regexp.MustCompile(`"esModuleInterop"\s*:\s*true`).Match(data)
					build.jsx = build.jsx || regexp.MustCompile(`"jsx"\s*:`).Match(data)
				}
				return base + extension, token, build
			}
		}
		return "", scriptToken{}, nil
	}
	return "", scriptToken{}, nil
}

func (b *typescriptBuild) packages() []string {
	packages := []string{babelifyPin, babelCore, babelIstanbul, "@babel/preset-env@^7"}
	if b.jsx {
		packages = append(packages, "@babel/preset-react@^7")
	}
	packages = append(packages, "@babel/preset-typescript@^7")
	if b.noInterop {
		packages = append(packages, "@babel/plugin-transform-modules-commonjs@^7")
	}
	return packages
}

func (b *typescriptBuild) babelConfig() string {
	env := `"@babel/preset-env"`
	plugins := `"istanbul"`
	if b.noInterop {
		env = `["@babel/preset-env", { "modules": false }]`
		plugins = `["@babel/plugin-transform-modules-commonjs", { "noInterop": true }], "istanbul"`
	}
	presets := env
	if b.jsx {
		presets += `, "@babel/preset-react"`
	}
	presets += `, "@babel/preset-typescript"`
	return "{\n  \"env\": {\n    \"test\": {\n      \"presets\": [" + presets + "],\n      \"plugins\": [" + plugins + "]\n    }\n  }\n}\n"
}

// coverageServe returns the exact change that serves the instrumented bundle
// to Cypress: a coverage variant of the script that builds and serves (such as
// start), and the cypress-io/github-action start input that runs it.
func coverageServe(root string, scripts map[string]string, build, coverage string) (string, []fileEdit) {
	fallback := fmt.Sprintf("Serve the bundle from `npm run %s` in the job that runs Cypress, instead of `npm run %s`; other builds stay uninstrumented.", coverage, build)
	reference := regexp.MustCompile(`(^|[\s&;|])npm run ` + regexp.QuoteMeta(build) + `($|[\s&;|])`)
	names := slices.Sorted(maps.Keys(scripts))
	if index := slices.Index(names, "start"); index > 0 {
		names = append([]string{"start"}, slices.Delete(names, index, index+1)...)
	}
	for _, name := range names {
		script := scripts[name]
		if name == build || strings.HasSuffix(name, ":coverage") || !reference.MatchString(script) {
			continue
		}
		serving := name + ":coverage"
		replaced := reference.ReplaceAllString(script, "${1}npm run "+coverage+"${2}")
		files := []fileEdit{{Path: "package.json", Action: "edit", Where: "add to scripts", Content: fmt.Sprintf("%q: %q\n", serving, replaced)}}
		invocations := []string{"npm run " + name}
		if name == "start" {
			invocations = append(invocations, "npm start")
		}
		workflows, _ := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.y*ml"))
		for _, workflow := range workflows {
			data, err := os.ReadFile(workflow)
			if err != nil || !strings.Contains(string(data), "cypress-io/github-action") {
				continue
			}
			for _, line := range strings.Split(string(data), "\n") {
				value, found := strings.CutPrefix(strings.TrimSpace(line), "start:")
				if !found || !slices.Contains(invocations, strings.Trim(strings.TrimSpace(value), `"'`)) {
					continue
				}
				relative, _ := filepath.Rel(root, workflow)
				files = append(files, fileEdit{Path: filepath.ToSlash(relative), Action: "edit", Where: "the start input of the cypress-io/github-action step; keep its indentation", Replace: strings.TrimSpace(line), Content: "start: npm run " + serving + "\n"})
				break
			}
		}
		return fmt.Sprintf("Serve the instrumented bundle to Cypress with `npm run %s`, which runs `npm run %s` instead of `npm run %s`; other builds stay uninstrumented.", serving, coverage, build), files
	}
	return fallback, nil
}

func firstExisting(root string, names ...string) string {
	for _, name := range names {
		if coverageFileExists(filepath.Join(root, name)) {
			return name
		}
	}
	return ""
}

func coverageFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func cypressCoverageEditReason(edit *coverageEdit) string {
	return fmt.Sprintf("No application source coverage observed, so Test Impact Analysis cannot be validated. ddtest recognized the %s build served to Cypress; the exact change is in the JSON report at runs[].coverage.edit of the run whose role is \"reporting-only\": install (%s), %d file edit(s), and how to serve the instrumented build. It is a ddtest-prescribed, test-only change: apply it exactly as given, without modifying or extending it, then rerun; the final report lists it with its revert.", edit.Build, edit.Install, len(edit.Files))
}

// viteIstanbulPlugin pins the vite-plugin-istanbul major whose peer range
// covers the installed Vite: 9 needs Vite 7 or later, 7 supports Vite 4-6.
// An unknown Vite (not installed) gets the current major; older Vite has none.
func viteIstanbulPlugin(viteMajor int) string {
	switch {
	case viteMajor == 0 || viteMajor >= 7:
		return "vite-plugin-istanbul@^9"
	case viteMajor >= 4:
		return "vite-plugin-istanbul@^7"
	}
	return ""
}

// installedMajor reads the major version of an installed package, or 0.
func installedMajor(root, name string) int {
	data, err := os.ReadFile(filepath.Join(root, "node_modules", name, "package.json"))
	if err != nil {
		return 0
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return 0
	}
	major, err := strconv.Atoi(strings.SplitN(manifest.Version, ".", 2)[0])
	if err != nil {
		return 0
	}
	return major
}
