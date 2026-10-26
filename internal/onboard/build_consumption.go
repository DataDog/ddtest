package onboard

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/kballard/go-shellquote"
)

// A CI build step is replayed before a test configuration only when the tests
// can reach what it produces. Proving the opposite is conservative: the build
// tool and every output directory must be known, every repository source file
// must be readable, and no import, self-reference or output path may reach an
// output. Anything uncertain keeps the build as a prerequisite.
const (
	maxConsumptionFiles = 20000
	maxConsumptionBytes = 64 << 20
	maxConsumptionFile  = 1 << 20
)

// Source files whose imports and path strings can reach a build output.
var consumptionExtensions = []string{".js", ".cjs", ".mjs", ".jsx", ".ts", ".cts", ".mts", ".tsx", ".vue", ".svelte"}

// Test-runner configuration stored as data rather than code.
var consumptionDataFiles = []string{".mocharc.json", ".mocharc.jsonc", ".mocharc.yaml", ".mocharc.yml", "cucumber.json", "cucumber.yaml", "cucumber.yml", "jest.config.json", "vitest.config.json"}

// Bundler configuration names its own outputs by design; it is not a consumer.
var bundlerConfigPrefixes = []string{"build.config.", "tsup.config.", "rollup.config.", "webpack.config."}

var (
	specifierPattern    = regexp.MustCompile(`(?:\bfrom\s*|\bimport\s*\(\s*|\bimport\s+|\brequire(?:\.resolve)?\s*\(\s*|\b(?:jest|vi)\.(?:mock|doMock|requireActual|importActual)\s*\(\s*)["'\x60]([^"'\x60\n]+)["'\x60]`)
	pathStringPattern   = regexp.MustCompile(`["'\x60]((?:<rootDir>/|\./|\.\./)?[^"'\x60\s]+)["'\x60]`)
	pathCallPattern     = regexp.MustCompile(`\b(?:path\.(?:join|resolve)|resolve|join|__dirname|import\.meta\.(?:url|dirname))\b`)
	literalOutDirConfig = regexp.MustCompile(`\boutDir\s*:\s*["'\x60]([^"'\x60]+)["'\x60]`)
	anyOutDirConfig     = regexp.MustCompile(`\boutDir\b`)
	rollupOutputConfig  = regexp.MustCompile(`\b(?:dir|file)\s*:\s*["'\x60]([^"'\x60]+)["'\x60]`)
	trailingJSONComma   = regexp.MustCompile(`,(\s*[}\]])`)
)

// consumedPreparation keeps the CI preparation a test configuration replays,
// dropping builds whose outputs the tests cannot reach and noting each one.
func consumedPreparation(root string, preparation []BuildCommand, location commandLocation, review *[]scopeDiagnostic) []BuildCommand {
	var kept []BuildCommand
	for _, build := range preparation {
		if consumed, outputs := buildConsumedByTests(root, build); !consumed {
			reason := "CI build is not replayed: no test, test configuration or self-reference reaches its outputs (" + strings.Join(outputs, ", ") + "): " + build.Command
			if !slices.ContainsFunc(*review, func(existing scopeDiagnostic) bool {
				return existing.Reason == reason && existing.Location.String() == location.String()
			}) {
				*review = append(*review, scopeDiagnostic{location, reason})
			}
			continue
		}
		kept = append(kept, build)
	}
	return kept
}

// buildConsumedByTests reports whether the tests may consume build's outputs.
// It returns false only when consumption is ruled out, with the outputs found.
func buildConsumedByTests(root string, build BuildCommand) (bool, []string) {
	relative := filepath.FromSlash(build.Directory)
	if build.Kind != "" || (relative != "." && !filepath.IsLocal(relative)) {
		return true, nil
	}
	directory := filepath.Join(root, relative)
	words, err := shellquote.Split(build.Command)
	if err != nil || len(words) == 0 {
		return true, nil
	}
	outputs, known := buildOutputs(directory, words)
	if !known || len(outputs) == 0 {
		return true, nil
	}
	var absolute []string
	for _, output := range outputs {
		path := filepath.Clean(filepath.Join(directory, filepath.FromSlash(output)))
		local, err := filepath.Rel(root, path)
		if err != nil || !filepath.IsLocal(local) {
			return true, nil
		}
		absolute = append(absolute, path)
	}
	slices.Sort(absolute)
	absolute = slices.Compact(absolute)
	if consumersFound(root, absolute, packageName(directory)) {
		return true, nil
	}
	var names []string
	for _, path := range absolute {
		local, _ := filepath.Rel(root, path)
		names = append(names, filepath.ToSlash(local))
	}
	return false, names
}

// buildOutputs lists the output paths a build tool writes, relative to its
// directory. known is false when configuration hides where outputs go.
func buildOutputs(directory string, words []string) ([]string, bool) {
	flag := func(names ...string) (string, bool) {
		for i := 1; i < len(words); i++ {
			for _, name := range names {
				if words[i] == name && i+1 < len(words) {
					return words[i+1], true
				}
				if value, ok := strings.CutPrefix(words[i], name+"="); ok {
					return value, true
				}
			}
		}
		return "", false
	}
	var outputs []string
	switch words[0] {
	case "tsc", "tsgo":
		if slices.Contains(words[1:], "--build") || slices.Contains(words[1:], "-b") {
			return nil, false
		}
		if value, ok := flag("--outDir"); ok {
			outputs = append(outputs, value)
			break
		}
		project := "tsconfig.json"
		if value, ok := flag("--project", "-p"); ok {
			project = value
			if info, err := os.Stat(filepath.Join(directory, project)); err == nil && info.IsDir() {
				project = filepath.Join(project, "tsconfig.json")
			}
		}
		outDir, ok := tsconfigOutDir(filepath.Join(directory, project))
		if !ok {
			return nil, false
		}
		outputs = append(outputs, filepath.Join(filepath.Dir(project), outDir))
	case "tsup", "unbuild":
		for i := 1; i < len(words); i++ {
			if words[i] == "-d" || words[i] == "--out-dir" {
				i++
				continue
			}
			// A positional argument selects other sources or a root directory.
			if !strings.HasPrefix(words[i], "-") {
				return nil, false
			}
		}
		if value, ok := flag("-d", "--out-dir"); ok {
			outputs = append(outputs, value)
		} else if outDir, ok := configuredOutDir(directory, words[0]); ok {
			outputs = append(outputs, outDir)
		} else {
			return nil, false
		}
		// Both infer entries from package.json and may write where it points.
		targets, ok := packageTargets(directory)
		if !ok {
			return nil, false
		}
		outputs = append(outputs, targets...)
	case "babel":
		value, ok := flag("-d", "--out-dir", "-o", "--out-file")
		if !ok {
			return nil, false
		}
		outputs = append(outputs, value)
	case "rollup":
		if value, ok := flag("-d", "--dir", "-o", "--file"); ok {
			outputs = append(outputs, value)
		} else if values, ok := rollupConfigOutputs(directory); ok {
			outputs = append(outputs, values...)
		} else {
			return nil, false
		}
	case "webpack":
		if value, ok := flag("--output-path", "-o"); ok {
			outputs = append(outputs, value)
		} else if !anyFileExists(directory, configNames("webpack.config.")) {
			outputs = append(outputs, "dist")
		} else {
			return nil, false
		}
	default:
		return nil, false
	}
	for _, output := range outputs {
		clean := filepath.Clean(filepath.FromSlash(output))
		if clean == "." || !filepath.IsLocal(clean) {
			return nil, false
		}
	}
	return outputs, true
}

func configNames(prefix string) []string {
	var names []string
	for _, extension := range []string{"ts", "js", "mjs", "cjs", "mts", "cts"} {
		names = append(names, prefix+extension)
	}
	return names
}

func anyFileExists(directory string, names []string) bool {
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(directory, name)); err == nil {
			return true
		}
	}
	return false
}

// configuredOutDir reads a literal outDir from tsup or unbuild configuration,
// falling back to the tool's documented dist default when none is set.
func configuredOutDir(directory, tool string) (string, bool) {
	prefix := map[string]string{"tsup": "tsup.config.", "unbuild": "build.config."}[tool]
	for _, name := range configNames(prefix) {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			continue
		}
		if matches := literalOutDirConfig.FindAllSubmatch(data, -1); len(matches) == 1 {
			return string(matches[0][1]), true
		}
		if anyOutDirConfig.Match(data) {
			return "", false
		}
		return "dist", true
	}
	var manifest map[string]json.RawMessage
	if data, err := os.ReadFile(filepath.Join(directory, "package.json")); err == nil && json.Unmarshal(data, &manifest) == nil {
		if value, ok := manifest[tool]; ok && anyOutDirConfig.Match(value) {
			return "", false
		}
	}
	return "dist", true
}

func rollupConfigOutputs(directory string) ([]string, bool) {
	for _, name := range configNames("rollup.config.") {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			continue
		}
		var outputs []string
		for _, match := range rollupOutputConfig.FindAllSubmatch(data, -1) {
			value := string(match[1])
			if slices.Contains([]string{".js", ".mjs", ".cjs"}, filepath.Ext(value)) {
				value = filepath.Dir(value)
			}
			outputs = append(outputs, value)
		}
		return outputs, len(outputs) > 0
	}
	return nil, false
}

// tsconfigOutDir reads compilerOptions.outDir. Without one, tsc emits next to
// the sources, so the outputs are not separable from the files tests import.
func tsconfigOutDir(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var config struct {
		CompilerOptions struct {
			OutDir string `json:"outDir"`
			NoEmit bool   `json:"noEmit"`
		} `json:"compilerOptions"`
	}
	if json.Unmarshal(stripJSONComments(data), &config) != nil || config.CompilerOptions.NoEmit || config.CompilerOptions.OutDir == "" {
		return "", false
	}
	return config.CompilerOptions.OutDir, true
}

// stripJSONComments removes comments and trailing commas outside strings, the
// tsconfig extensions to JSON.
func stripJSONComments(data []byte) []byte {
	var out []byte
	inString := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out = append(out, c)
			if c == '\\' && i+1 < len(data) {
				i++
				out = append(out, data[i])
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			out = append(out, c)
		case c == '/' && i+1 < len(data) && data[i+1] == '/':
			for i < len(data) && data[i] != '\n' {
				i++
			}
			out = append(out, '\n')
		case c == '/' && i+1 < len(data) && data[i+1] == '*':
			i += 2
			for i+1 < len(data) && (data[i] != '*' || data[i+1] != '/') {
				i++
			}
			i++
		default:
			out = append(out, c)
		}
	}
	return trailingJSONComma.ReplaceAll(out, []byte("$1"))
}

// packageTargets lists the top-level directories package.json entry points
// name. A target at the package root cannot be told apart from sources.
func packageTargets(directory string) ([]string, bool) {
	data, err := os.ReadFile(filepath.Join(directory, "package.json"))
	if err != nil {
		return nil, true
	}
	var manifest map[string]any
	if json.Unmarshal(data, &manifest) != nil {
		return nil, false
	}
	var targets []string
	var collect func(any)
	collect = func(value any) {
		switch value := value.(type) {
		case string:
			targets = append(targets, value)
		case map[string]any:
			for _, item := range value {
				collect(item)
			}
		case []any:
			for _, item := range value {
				collect(item)
			}
		}
	}
	for _, key := range []string{"main", "module", "browser", "types", "typings", "exports", "bin"} {
		collect(manifest[key])
	}
	var directories []string
	for _, target := range targets {
		clean := filepath.Clean(filepath.FromSlash(target))
		if clean == "package.json" || strings.Contains(clean, "*") {
			continue
		}
		dir := filepath.Dir(clean)
		if dir == "." || !filepath.IsLocal(dir) {
			return nil, false
		}
		directories = append(directories, strings.Split(dir, string(filepath.Separator))[0])
	}
	slices.Sort(directories)
	return slices.Compact(directories), true
}

func packageName(directory string) string {
	var manifest struct {
		Name string `json:"name"`
	}
	data, err := os.ReadFile(filepath.Join(directory, "package.json"))
	if err != nil || json.Unmarshal(data, &manifest) != nil {
		return ""
	}
	return manifest.Name
}

// consumersFound scans repository sources for an import, self-reference or
// path string that reaches an output. Limits, unreadable files and a tree with
// no source to inspect count as consumers, so the build stays a prerequisite.
func consumersFound(root string, outputs []string, name string) bool {
	inside := func(path string) bool {
		for _, output := range outputs {
			if path == output || strings.HasPrefix(path, output+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	files, sources, total := 0, 0, int64(0)
	found := false
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		base := entry.Name()
		if entry.IsDir() {
			if path != root && (base == "node_modules" || strings.HasPrefix(base, ".") || inside(path)) {
				return filepath.SkipDir
			}
			return nil
		}
		manifest := base == "package.json"
		if !manifest && !slices.Contains(consumptionDataFiles, base) && !slices.Contains(consumptionExtensions, filepath.Ext(base)) {
			return nil
		}
		if slices.ContainsFunc(bundlerConfigPrefixes, func(prefix string) bool { return strings.HasPrefix(base, prefix) }) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files++
		if slices.Contains(consumptionExtensions, filepath.Ext(base)) {
			sources++
		}
		total += info.Size()
		if files > maxConsumptionFiles || total > maxConsumptionBytes || info.Size() > maxConsumptionFile {
			found = true
			return filepath.SkipAll
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if manifest {
			content = testRunnerManifestFields(content)
		}
		if reachesOutput(filepath.Dir(path), string(content), outputs, name, inside) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found || err != nil || sources == 0
}

// testRunnerManifestFields keeps only package.json fields a test runner reads;
// entry points and scripts name build outputs without consuming them.
func testRunnerManifestFields(content []byte) []byte {
	var manifest map[string]json.RawMessage
	if json.Unmarshal(content, &manifest) != nil {
		return content
	}
	var out []byte
	for _, key := range []string{"jest", "mocha", "vitest", "ava", "c8", "nyc", "imports"} {
		out = append(append(out, manifest[key]...), '\n')
	}
	return out
}

func reachesOutput(directory, content string, outputs []string, name string, inside func(string) bool) bool {
	resolve := func(value string) bool {
		if rooted, ok := strings.CutPrefix(value, "<rootDir>/"); ok {
			return inside(filepath.Clean(filepath.Join(directory, filepath.FromSlash(rooted))))
		}
		if strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") || strings.Contains(value, "/") {
			return inside(filepath.Clean(filepath.Join(directory, filepath.FromSlash(value))))
		}
		return false
	}
	for _, match := range specifierPattern.FindAllStringSubmatch(content, -1) {
		specifier := match[1]
		if name != "" && (specifier == name || strings.HasPrefix(specifier, name+"/")) {
			return true
		}
		if resolve(specifier) {
			return true
		}
	}
	for _, line := range strings.Split(content, "\n") {
		for _, match := range pathStringPattern.FindAllStringSubmatch(line, -1) {
			value := match[1]
			if resolve(value) {
				return true
			}
			// path.join(__dirname, "..", "dist") names an output by its bare segment.
			if !strings.Contains(value, "/") && pathCallPattern.MatchString(line) {
				for _, output := range outputs {
					if filepath.Base(output) == value {
						return true
					}
				}
			}
		}
	}
	return false
}
