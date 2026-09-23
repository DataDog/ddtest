package jsconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

type JestOptions struct {
	Directory string
	Args      []string
	Env       map[string]string
}

// JestPlan preserves matching semantics instead of translating everything to
// DDTest's glob flags. Plans are rebuilt per invocation, with no stale cache.
type JestPlan struct {
	directory string
	projects  []jestProject
	overrides object
}

type jestProject struct {
	roots       []string
	extensions  []string
	globs       []glob
	regexes     []*regexp.Regexp
	ignores     []*regexp.Regexp
	cliPatterns []*regexp.Regexp
}

func CompileJest(ctx context.Context, options JestOptions) (*JestPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cwd := options.Directory
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	configPath, overrides, patterns, err := jestCLI(options.Args)
	if err != nil {
		return nil, err
	}
	e := newEvaluator(ctx, cwd, options.Env)
	config, base, err := e.jestConfig(cwd, configPath)
	if err != nil {
		return nil, err
	}
	maps.Copy(config, overrides)
	major, err := jestMajor(cwd)
	if err != nil {
		return nil, err
	}
	plan := &JestPlan{directory: cwd, overrides: overrides}
	if err := plan.addProject(e, config, base, major, patterns, 0); err != nil {
		return nil, err
	}
	return plan, nil
}

func (e *evaluator) jestConfig(directory, configured string) (object, string, error) {
	if strings.HasPrefix(configured, "{") {
		var config object
		err := json.Unmarshal([]byte(configured), &config)
		return config, directory, err
	}
	if configured != "" {
		if !filepath.IsAbs(configured) {
			configured = filepath.Join(directory, configured)
		}
		if info, err := os.Stat(configured); err == nil && info.IsDir() {
			return e.jestConfig(configured, "")
		}
		return e.readJestConfig(configured)
	}
	start := directory
	for {
		paths := []string{}
		for _, name := range []string{"jest.config.js", "jest.config.ts", "jest.config.mjs", "jest.config.mts", "jest.config.cjs", "jest.config.cts", "jest.config.json"} {
			path := filepath.Join(directory, name)
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				paths = append(paths, path)
			}
		}
		pkgPath := filepath.Join(directory, "package.json")
		pkgData, pkgErr := os.ReadFile(pkgPath)
		if pkgErr == nil {
			var pkg object
			if err := json.Unmarshal(pkgData, &pkg); err != nil {
				return nil, "", err
			}
			if v, ok := pkg["jest"]; ok && v != nil {
				paths = append(paths, pkgPath)
			}
		} else if !os.IsNotExist(pkgErr) {
			return nil, "", pkgErr
		}
		if len(paths) > 1 {
			return nil, "", fmt.Errorf("multiple Jest configurations in %s", directory)
		}
		if len(paths) == 1 {
			return e.readJestConfig(paths[0])
		}
		if pkgErr == nil {
			return object{}, directory, nil
		}
		if filepath.Dir(directory) == directory {
			return object{}, start, nil
		}
		directory = filepath.Dir(directory)
	}
}

func (e *evaluator) readJestConfig(path string) (object, string, error) {
	v, err := e.load(path)
	if err != nil {
		return nil, "", err
	}
	m, ok := v.(object)
	if !ok {
		return nil, "", fmt.Errorf("jest config must resolve to an object: %s", path)
	}
	if filepath.Base(path) == "package.json" {
		m, ok = m["jest"].(object)
		if !ok {
			return nil, "", fmt.Errorf("package.json jest must be an object")
		}
	}
	return m, filepath.Dir(path), nil
}

func (p *JestPlan) addProject(e *evaluator, config object, base string, major int, patterns []string, depth int) error {
	if depth > 16 {
		return fmt.Errorf("too many nested Jest projects")
	}
	config = maps.Clone(config)
	maps.Copy(config, p.overrides)
	root := base
	if v, ok := config["rootDir"]; ok {
		value, err := stringValue(v)
		if err != nil {
			return err
		}
		root = absolute(base, value)
	}
	config = maps.Clone(config)
	config["rootDir"] = root
	if preset, ok := config["preset"]; ok && preset != nil {
		name, err := stringValue(preset)
		if err != nil {
			return err
		}
		path, err := jestPreset(root, name)
		if err != nil {
			return err
		}
		v, err := e.load(path)
		if err != nil {
			return err
		}
		m, ok := v.(object)
		if !ok {
			return fmt.Errorf("jest preset is not a static object")
		}
		if m["preset"] != nil {
			return fmt.Errorf("nested Jest presets need native discovery")
		}
		merged := maps.Clone(m)
		maps.Copy(merged, config)
		if m["modulePathIgnorePatterns"] != nil && config["modulePathIgnorePatterns"] != nil {
			presetIgnores, err := asStrings(m["modulePathIgnorePatterns"])
			if err != nil {
				return err
			}
			ownIgnores, err := asStrings(config["modulePathIgnorePatterns"])
			if err != nil {
				return err
			}
			combined := []any{}
			for _, value := range append(presetIgnores, ownIgnores...) {
				combined = append(combined, value)
			}
			merged["modulePathIgnorePatterns"] = combined
		}
		config = merged
	}
	// These options can modify the candidate set in arbitrary code or introduce
	// version-specific crawling behavior. Never infer their behavior.
	for _, key := range []string{"filter", "haste", "testSequencer", "runner", "watchPlugins", "dependencyExtractor"} {
		if v, ok := config[key]; ok && v != nil {
			return fmt.Errorf("jest %s requires native discovery", key)
		}
	}
	if projects, ok := config["projects"]; ok {
		items, ok := projects.([]any)
		if !ok {
			return fmt.Errorf("jest projects must be a static array")
		}
		if len(items) > 0 {
			if depth > 0 {
				return fmt.Errorf("nested projects need native discovery")
			}
			for _, item := range items {
				switch child := item.(type) {
				case object:
					// Jest resolves an inline project's root against the config
					// file directory, not against the parent's rootDir. Project
					// discovery options are independent of parent options.
					if err := p.addProject(e, child, base, major, patterns, depth+1); err != nil {
						return err
					}
				case string:
					if strings.ContainsAny(child, "!()") {
						return fmt.Errorf("project glob needs native discovery")
					}
					pattern := absolute(p.directory, strings.ReplaceAll(child, "<rootDir>", root))
					paths, err := doublestar.FilepathGlob(pattern)
					if err != nil {
						return err
					}
					if len(paths) == 0 {
						return fmt.Errorf("jest project did not resolve: %s", child)
					}
					for _, path := range paths {
						m, dir, err := e.jestConfig(root, path)
						if err != nil {
							return err
						}
						if err := p.addProject(e, m, dir, major, patterns, depth+1); err != nil {
							return err
						}
					}
				default:
					return fmt.Errorf("unsupported Jest project")
				}
			}
			return nil
		}
	}
	roots, err := stringsField(config, "roots", []string{"<rootDir>"})
	if err != nil {
		return err
	}
	for i, r := range roots {
		roots[i] = absolute(root, strings.ReplaceAll(r, "<rootDir>", root))
		if resolved, err := filepath.EvalSymlinks(roots[i]); err == nil && resolved != roots[i] {
			return fmt.Errorf("symlinked Jest root needs native discovery: %s", roots[i])
		}
		relative, err := filepath.Rel(p.directory, roots[i])
		if err != nil {
			return err
		}
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("jest roots outside the working directory need native discovery")
		}
		info, err := os.Stat(roots[i])
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("jest root is not a directory: %s", roots[i])
		}
	}
	extensions := []string{"js", "json", "jsx", "ts", "tsx", "node"}
	if major < 24 {
		extensions = []string{"js", "json", "jsx", "node"}
	}
	if major >= 28 {
		extensions = append(extensions, "mjs", "cjs")
	}
	if major >= 30 {
		extensions = append(extensions, "mts", "cts")
	}
	extensions, err = stringsField(config, "moduleFileExtensions", extensions)
	if err != nil {
		return err
	}
	for _, extension := range extensions {
		if extension == "" || strings.ContainsAny(extension, ".*+?[](){}\\/|") {
			return fmt.Errorf("nonstandard moduleFileExtensions need native discovery")
		}
	}
	defaultMatch := []string{"**/__tests__/**/*.[jt]s?(x)", "**/?(*.)+(spec|test).[tj]s?(x)"}
	if major < 24 {
		defaultMatch = []string{"**/__tests__/**/*.js?(x)", "**/?(*.)@(spec|test).js?(x)"}
	}
	if major >= 30 {
		defaultMatch = []string{"**/__tests__/**/*.?([mc])[jt]s?(x)", "**/?(*.)+(spec|test).?([mc])[tj]s?(x)"}
	}
	regexes, err := stringsField(config, "testRegex", nil)
	if err != nil {
		return err
	}
	if len(regexes) == 1 && regexes[0] == "" {
		regexes = nil
	}
	if len(regexes) > 0 {
		defaultMatch = nil
		if matches, ok := config["testMatch"]; ok {
			values, err := asStrings(matches)
			if err != nil {
				return err
			}
			if len(values) > 0 {
				return fmt.Errorf("jest testMatch and testRegex cannot both be set")
			}
		}
	}
	matches, err := stringsField(config, "testMatch", defaultMatch)
	if err != nil {
		return err
	}
	ignores, err := stringsField(config, "testPathIgnorePatterns", []string{"/node_modules/"})
	if err != nil {
		return err
	}
	moduleIgnores, err := stringsField(config, "modulePathIgnorePatterns", nil)
	if err != nil {
		return err
	}
	ignores = append(ignores, moduleIgnores...)
	if directory, ok := config["cacheDirectory"]; ok {
		path, err := stringValue(directory)
		if err != nil {
			return err
		}
		path = absolute(root, strings.ReplaceAll(path, "<rootDir>", root))
		ignores = append(ignores, regexp.QuoteMeta(filepath.ToSlash(path))+"/")
	}
	for _, values := range [][]string{matches, ignores} {
		for i, value := range values {
			values[i] = strings.ReplaceAll(value, "<rootDir>", filepath.ToSlash(root))
		}
	}
	globs, err := compileGlobs(matches)
	if err != nil {
		return err
	}
	testRegexes, err := compileRegexes(regexes)
	if err != nil {
		return err
	}
	ignoreRegexes, err := compileRegexes(ignores)
	if err != nil {
		return err
	}
	cliRegexes, err := compileRegexes(patterns)
	if err != nil {
		return err
	}
	p.projects = append(p.projects, jestProject{roots, extensions, globs, testRegexes, ignoreRegexes, cliRegexes})
	return nil
}

func (p *JestPlan) Discover(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	files := map[string]struct{}{}
	for _, project := range p.projects {
		for _, root := range project.roots {
			err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() {
					if entry.Name() == "node_modules" || entry.Name() == ".git" || entry.Name() == ".hg" || entry.Name() == ".sl" {
						return filepath.SkipDir
					}
					return nil
				}
				// Jest's default crawler does not follow symlinks.
				if entry.Type()&os.ModeSymlink != 0 {
					return nil
				}
				if !slices.Contains(project.extensions, strings.TrimPrefix(filepath.Ext(path), ".")) {
					return nil
				}
				path = filepath.ToSlash(path)
				for _, char := range path {
					if char > 0xffff {
						return fmt.Errorf("non-BMP filename needs native JavaScript matching")
					}
				}
				if strings.ContainsAny(path, "\r\n\u2028\u2029") {
					return fmt.Errorf("newline in test path requires native discovery")
				}
				if matchRegexes(project.ignores, path) {
					return nil
				}
				if len(project.globs) > 0 && !matchGlobs(project.globs, path) {
					return nil
				}
				if len(project.regexes) > 0 && !matchRegexes(project.regexes, path) {
					return nil
				}
				if len(project.cliPatterns) > 0 && !matchRegexes(project.cliPatterns, path) {
					return nil
				}
				relative, err := filepath.Rel(p.directory, filepath.FromSlash(path))
				if err != nil {
					return err
				}
				files[filepath.ToSlash(relative)] = struct{}{}
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
	}
	return slices.Sorted(maps.Keys(files)), nil
}

func absolute(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, path)
}

func stringsField(config object, key string, fallback []string) ([]string, error) {
	v, ok := config[key]
	if !ok || v == nil {
		return slices.Clone(fallback), nil
	}
	values, err := asStrings(v)
	if err != nil {
		return nil, fmt.Errorf("jest %s: %w", key, err)
	}
	return values, nil
}

func asStrings(value any) ([]string, error) {
	if v, ok := value.(string); ok {
		return []string{v}, nil
	}
	values, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("expected a string or static array")
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		v, err := stringValue(value)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, nil
}

func jestMajor(cwd string) (int, error) {
	for dir := cwd; ; dir = filepath.Dir(dir) {
		data, err := os.ReadFile(filepath.Join(dir, "node_modules/jest/package.json"))
		if err == nil {
			var pkg struct{ Version string }
			if err := json.Unmarshal(data, &pkg); err != nil {
				return 0, err
			}
			major, _, _ := strings.Cut(pkg.Version, ".")
			v, err := strconv.Atoi(major)
			if err != nil || v < 22 || v > 30 {
				return 0, fmt.Errorf("unmodeled Jest version %s", pkg.Version)
			}
			return v, nil
		}
		if !os.IsNotExist(err) {
			return 0, err
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	// No installed Jest: still support file-only planning with current defaults.
	return 30, nil
}

func jestPreset(root, name string) (string, error) {
	name = strings.ReplaceAll(name, "<rootDir>", root)
	bases := []string{}
	if strings.HasPrefix(name, ".") || filepath.IsAbs(name) {
		bases = append(bases, absolute(root, name))
	} else {
		for dir := root; ; dir = filepath.Dir(dir) {
			bases = append(bases, filepath.Join(dir, "node_modules", name))
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	for _, base := range bases {
		if info, err := os.Stat(base); err == nil && !info.IsDir() {
			return base, nil
		}
		for _, file := range []string{"jest-preset.js", "jest-preset.cjs", "jest-preset.mjs", "jest-preset.json"} {
			candidate := filepath.Join(base, file)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("cannot statically resolve Jest preset %q", name)
}

func jestCLI(args []string) (string, object, []string, error) {
	config := ""
	overrides := object{}
	patterns := []string{}
	boolFlags := []string{"ci", "runInBand", "silent", "verbose", "json", "coverage", "collectCoverage", "no-coverage", "passWithNoTests", "detectOpenHandles", "forceExit", "logHeapUsage", "no-cache", "cache", "colors", "color", "no-colors", "expand", "updateSnapshot", "u", "errorOnDeprecated"}
	valueFlags := []string{"outputFile", "maxWorkers", "w", "maxConcurrency", "testTimeout", "testNamePattern", "t", "coverageDirectory", "coverageProvider", "testResultsProcessor", "testEnvironment", "env", "testRunner", "testURL"}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			patterns = append(patterns, arg)
			continue
		}
		key, value, inline := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if slices.Contains(boolFlags, key) {
			if !inline && i+1 < len(args) && (args[i+1] == "true" || args[i+1] == "false") {
				i++
			}
			continue
		}
		if key == "watch" || key == "watchAll" {
			if inline && value == "false" {
				continue
			}
			return "", nil, nil, fmt.Errorf("jest watch mode needs native discovery")
		}
		array := slices.Contains([]string{"roots", "testMatch", "testRegex", "testPathIgnorePatterns", "modulePathIgnorePatterns", "moduleFileExtensions", "testPathPatterns"}, key)
		if key == "config" || key == "c" || key == "rootDir" || key == "testPathPattern" || array || slices.Contains(valueFlags, key) {
			if !inline {
				i++
				if i == len(args) {
					return "", nil, nil, fmt.Errorf("jest --%s has no value", key)
				}
				value = args[i]
			}
			values := []any{value}
			if array && !inline {
				for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					i++
					values = append(values, args[i])
				}
			}
			switch key {
			case "config", "c":
				config = value
			case "rootDir":
				overrides[key] = value
			case "testPathPattern", "testPathPatterns":
				for _, v := range values {
					patterns = append(patterns, v.(string))
				}
			default:
				if array {
					overrides[key] = values
				}
			}
			continue
		}
		return "", nil, nil, fmt.Errorf("jest --%s needs native discovery", key)
	}
	return config, overrides, patterns, nil
}
