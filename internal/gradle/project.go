// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package gradle reads Gradle builds and command lines statically, without
// running Gradle or any build logic.
package gradle

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/maven"
	toml "github.com/pelletier/go-toml/v2"
)

// maxProjects bounds the projects one settings file may include.
const maxProjects = 256

// SettingsFiles and BuildFiles are the Groovy and Kotlin names Gradle reads,
// in its order of precedence when both exist.
var (
	SettingsFiles = []string{"settings.gradle", "settings.gradle.kts"}
	BuildFiles    = []string{"build.gradle", "build.gradle.kts"}
)

// Declared reports whether directory holds a Gradle settings or build file.
func Declared(directory string) bool {
	return slices.ContainsFunc(append(slices.Clone(SettingsFiles), BuildFiles...), func(name string) bool {
		info, err := os.Stat(filepath.Join(directory, name))
		return err == nil && info.Mode().IsRegular()
	})
}

// Project is one project of a Gradle build, read as written: plugins,
// convention plugins and other build logic are not evaluated, so dependencies
// they add are not seen.
type Project struct {
	// Path is the Gradle project path, as :app; the root project's is :.
	Path string
	// Directory and BuildFile are relative to the inspected root; BuildFile is
	// empty when the project has none.
	Directory, BuildFile string
	Dependencies         []maven.Dependency
}

// Frameworks returns the supported test frameworks the project declares. A
// declaration does not prove execution.
func (p Project) Frameworks() []string {
	var names []string
	for _, dependency := range p.Dependencies {
		if name := dependency.Framework(); name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// Frameworks returns the supported test frameworks declared anywhere in the build.
func Frameworks(projects []Project) []string {
	var names []string
	for _, project := range projects {
		for _, name := range project.Frameworks() {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	slices.Sort(names)
	return names
}

// Build finds the build a command run in directory belongs to: the nearest
// directory, up to root, with a settings file, else directory itself as a
// single-project build. Paths are relative to root.
func Build(root, directory string) (string, error) {
	for current := filepath.Clean(directory); ; current = filepath.Dir(current) {
		if !filepath.IsLocal(current) && current != "." {
			return "", fmt.Errorf("gradle build directory must stay inside the repository: %s", directory)
		}
		for _, name := range SettingsFiles {
			if info, err := os.Stat(filepath.Join(root, current, name)); err == nil && info.Mode().IsRegular() {
				return current, nil
			}
		}
		if current == "." {
			return filepath.Clean(directory), nil
		}
	}
}

// Projects reads the build rooted at build (relative to root): the root
// project, then each project its settings file includes, in order. A project
// list computed by build logic cannot be read statically and is an error.
func Projects(root, build string) ([]Project, error) {
	settings, err := readFirst(root, build, SettingsFiles)
	if err != nil {
		return nil, err
	}
	if settings, err = appliedSettings(root, build, settings, 0); err != nil {
		return nil, fmt.Errorf("gradle settings in %s: %w", build, err)
	}
	paths, directories, err := settingsProjects(settings)
	if err != nil {
		return nil, fmt.Errorf("gradle settings in %s: %w", build, err)
	}
	catalog, err := readCatalog(root, build)
	if err != nil {
		return nil, err
	}
	projects := make([]Project, 0, len(paths)+1)
	for _, path := range append([]string{":"}, paths...) {
		if filepath.IsAbs(directories[path]) {
			return nil, fmt.Errorf("gradle project %s is outside the repository: %s", path, directories[path])
		}
		directory := filepath.Join(build, directories[path])
		if err := insideRoot(root, directory); err != nil {
			return nil, fmt.Errorf("gradle project %s %w", path, err)
		}
		project := Project{Path: path, Directory: directory}
		for _, name := range BuildFiles {
			file := filepath.Join(directory, name)
			data, err := readInside(root, file)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			project.BuildFile = file
			project.Dependencies = buildDependencies(stripComments(string(data)), catalog)
			break
		}
		projects = append(projects, project)
	}
	return projects, nil
}

// readInside reads a file, relative to root, that stays inside root once
// symlinks resolve: a build file that links outside the repository is not
// read.
func readInside(root, file string) ([]byte, error) {
	if err := insideRoot(root, file); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		return nil, err
	}
	return data, nil
}

// insideRoot checks that a directory or file, relative to root, stays inside
// root once symlinks resolve. One that does not exist reads no build file.
func insideRoot(root, directory string) error {
	if !filepath.IsLocal(directory) && directory != "." {
		return fmt.Errorf("leaves the repository: %s", directory)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, directory))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if relative, err := filepath.Rel(canonical, resolved); err != nil || !filepath.IsLocal(relative) && relative != "." {
		return fmt.Errorf("leaves the repository through a symlink: %s", directory)
	}
	return nil
}

// testTaskDeclarations declare a Test task or JVM test suite, as opposed to
// configuring existing ones with withType<Test>, named or testing { suites {
// test { … } } }.
var testTaskDeclarations = regexp.MustCompile(`(?:register|create|maybeCreate)\s*<\s*(?:Test|JvmTestSuite)\s*>|` +
	`(?:register|create|maybeCreate)\s*\([^()]*?,\s*(?:Test|JvmTestSuite)(?:::class(?:\.java)?)?\s*[,)]|` +
	`\btype\s*:\s*Test\b|` +
	`(?:registering|creating)\s*\(\s*(?:Test|JvmTestSuite)::class|` +
	`\(\s*JvmTestSuite\s*\)`)

// testTaskNames capture the name of a declaration testTaskDeclarations finds,
// when it is a literal: tasks.register<Test>("integrationTest"),
// register("it", Test), task it(type: Test), val it by registering(Test::class)
// or integrationTest(JvmTestSuite).
var testTaskNames = []*regexp.Regexp{
	regexp.MustCompile(`(?:register|create|maybeCreate)\s*<\s*(?:Test|JvmTestSuite)\s*>\s*\(\s*["'](\w+)["']`),
	regexp.MustCompile(`(?:register|create|maybeCreate)\s*\(\s*["'](\w+)["']\s*,\s*(?:Test|JvmTestSuite)(?:::class(?:\.java)?)?\s*[,)]`),
	regexp.MustCompile(`\btask\s+(\w+)\s*\(\s*type\s*:\s*Test\b`),
	regexp.MustCompile(`\bval\s+(\w+)\s+by\s+(?:tasks\.)?(?:registering|creating)\s*\(\s*(?:Test|JvmTestSuite)::class`),
	regexp.MustCompile(`\b(\w+)\s*\(\s*JvmTestSuite\s*\)`),
}

// declaredTestTasks returns the test task names a build file's source declares
// literally, and whether it has a declaration whose name is computed.
func declaredTestTasks(source string) (names []string, computed bool) {
	var spans [][]int
	for _, pattern := range testTaskNames {
		for _, match := range pattern.FindAllStringSubmatchIndex(source, -1) {
			spans = append(spans, match)
			if name := source[match[2]:match[3]]; !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	// Every declaration must be one a literal name was read from.
	for _, declaration := range testTaskDeclarations.FindAllStringIndex(source, -1) {
		if !slices.ContainsFunc(spans, func(span []int) bool { return span[0] <= declaration[0] && declaration[1] <= span[1] }) {
			computed = true
		}
	}
	return names, computed
}

// TestTasks are the test tasks a build declares besides test, by name, and
// whether it may declare others ddtest cannot name: in build logic ddtest
// does not read, such as buildSrc or an included build, or in a declaration
// it cannot follow.
type TestTasks struct {
	Declared []string
	Unread   bool
}

// Runs reports whether a task runs tests: TestTask, or a declared test task.
func (t TestTasks) Runs(task string) bool {
	return TestTask(task) || slices.Contains(t.Declared, taskName(task))
}

// Others reports whether the build may have test tasks besides test, which a
// lifecycle task such as check runs even with -x test.
func (t TestTasks) Others() bool {
	return len(t.Declared) > 0 || t.Unread
}

// DeclaredTestTasks reads the test tasks a build's projects declare.
func DeclaredTestTasks(root, build string, projects []Project) TestTasks {
	var tasks TestTasks
	if info, err := os.Stat(filepath.Join(root, build, "buildSrc")); err == nil && info.IsDir() {
		tasks.Unread = true
	}
	if settings, err := readFirst(root, build, SettingsFiles); err != nil || strings.Contains(settings, "includeBuild") {
		tasks.Unread = true
	}
	for _, project := range projects {
		if project.BuildFile == "" {
			continue
		}
		data, err := readInside(root, project.BuildFile)
		if err != nil {
			tasks.Unread = true
			continue
		}
		names, computed := declaredTestTasks(stripComments(string(data)))
		tasks.Unread = tasks.Unread || computed
		for _, name := range names {
			if !slices.Contains(tasks.Declared, name) {
				tasks.Declared = append(tasks.Declared, name)
			}
		}
	}
	slices.Sort(tasks.Declared)
	return tasks
}

// maxAppliedDepth bounds scripts that settings apply from applied scripts.
const maxAppliedDepth = 3

// appliedSettings replaces each script the settings apply, at a literal path
// inside the repository, with its contents, so the projects it includes are
// read. A script at a path ddtest cannot locate is left for settingsProjects
// to reject.
func appliedSettings(root, build, settings string, depth int) (string, error) {
	var failure error
	expanded := appliedScript.ReplaceAllStringFunc(settings, func(statement string) string {
		match := appliedScript.FindStringSubmatch(statement)
		path := match[1] + match[2]
		for _, prefix := range []string{"$rootDir/", "${rootDir}/", "$settingsDir/", "${settingsDir}/"} {
			path = strings.TrimPrefix(path, prefix)
		}
		if strings.ContainsAny(path, "$") || strings.Contains(path, "://") || filepath.IsAbs(path) || depth >= maxAppliedDepth {
			return statement
		}
		data, err := readInside(root, filepath.Join(build, filepath.FromSlash(path)))
		if err != nil {
			failure = errors.Join(failure, fmt.Errorf("apply %s: %w", path, err))
			return statement
		}
		nested, err := appliedSettings(root, build, stripComments(string(data)), depth+1)
		failure = errors.Join(failure, err)
		return "\n" + nested + "\n"
	})
	return expanded, failure
}

// DefaultTasks reports whether a command without tasks, run in directory, may
// run default tasks: its project's build file declares them, or its build's
// root build file does, as in subprojects { defaultTasks("test") }. A file
// that cannot be read counts as declaring them.
func DefaultTasks(root, directory string) bool {
	build, err := Build(root, directory)
	if err != nil {
		return true
	}
	for _, current := range slices.Compact([]string{filepath.Clean(directory), build}) {
		for _, name := range BuildFiles {
			data, err := readInside(root, filepath.Join(current, name))
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil || strings.Contains(stripComments(string(data)), "defaultTasks") {
				return true
			}
			break
		}
	}
	return false
}

// readFirst reads the first of names in directory that exists; empty when none does.
func readFirst(root, directory string, names []string) (string, error) {
	for _, name := range names {
		data, err := readInside(root, filepath.Join(directory, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		return stripComments(string(data)), nil
	}
	return "", nil
}

var (
	// include ':a', ':b' or include(":a", ":b"), up to the end of its argument
	// list: a closing parenthesis, or a line that does not end with a comma.
	// Anywhere, so that an include inside a block or lambda is not missed;
	// includeBuild and other names that start with include are not matched.
	includeStatement = regexp.MustCompile(`\binclude(?:[ \t]*(\()|[ \t]+)`)
	// A quoted project path; only horizontal space after it, so that the end
	// of an unparenthesized statement stays visible.
	quoted = regexp.MustCompile(`^\s*(?:"([^"$]*)"|'([^']*)')[ \t]*`)
	// project(":a").projectDir = file("dir"), in either quote style.
	projectDirectory = regexp.MustCompile(`project\(\s*["']([^"']+)["']\s*\)\.projectDir\s*=\s*file\(\s*["']([^"'$]+)["']\s*\)`)
	otherInclusion   = regexp.MustCompile(`\b(?:includeFlat|projectDir\s*=)`)
	// apply from: 'projects.gradle' or apply(from = "projects.gradle.kts")
	// runs another script, which may include projects; buildFileName renames
	// a project's build file.
	settingsDelegation = regexp.MustCompile(`\bapply\s*\(?\s*from\s*[:=]|\bbuildFileName\s*=`)
	// An applied script ddtest can locate: a literal path, relative to the
	// settings or to $rootDir.
	appliedScript = regexp.MustCompile(`\bapply\s*\(?\s*from\s*[:=]\s*(?:"([^"]*)"|'([^']*)')\s*\)?`)
)

// settingsProjects lists the project paths a settings file includes, with
// each project's directory relative to the build, implicit parents included.
func settingsProjects(settings string) ([]string, map[string]string, error) {
	if match := settingsDelegation.FindString(settings); match != "" {
		return nil, nil, fmt.Errorf("settings %s, which ddtest does not read", map[bool]string{true: "rename project build files", false: "apply a script ddtest cannot locate"}[strings.Contains(match, "buildFileName")])
	}
	var paths []string
	directories := map[string]string{":": "."}
	add := func(path string) {
		path = ":" + strings.TrimPrefix(path, ":")
		// Including :a:b includes :a too.
		segments := strings.Split(strings.TrimPrefix(path, ":"), ":")
		for i := range segments {
			parent := ":" + strings.Join(segments[:i+1], ":")
			if _, known := directories[parent]; !known {
				paths = append(paths, parent)
				directories[parent] = filepath.Join(segments[:i+1]...)
			}
		}
	}
	for _, match := range includeStatement.FindAllStringSubmatchIndex(settings, -1) {
		parenthesized := match[2] >= 0
		rest := settings[match[1]:]
		for {
			value := quoted.FindStringSubmatch(rest)
			if value == nil {
				return nil, nil, fmt.Errorf("include lists projects that build logic computes")
			}
			add(value[1] + value[2])
			rest = rest[len(value[0]):]
			if strings.HasPrefix(rest, ",") {
				rest = rest[1:]
				// Kotlin allows a trailing comma before the parenthesis.
				if trimmed := strings.TrimLeft(rest, " \t\r\n"); parenthesized && strings.HasPrefix(trimmed, ")") {
					break
				}
				continue
			}
			// The argument list must end here: anything else, such as
			// ':module-' + name, computes the path.
			if parenthesized && !strings.HasPrefix(strings.TrimLeft(rest, " \t\r\n"), ")") ||
				!parenthesized && rest != "" && !strings.ContainsAny(rest[:1], "\r\n;}") {
				return nil, nil, fmt.Errorf("include lists projects that build logic computes")
			}
			break
		}
		if len(paths) > maxProjects {
			return nil, nil, fmt.Errorf("settings include more than %d projects", maxProjects)
		}
	}
	for _, index := range projectDirectory.FindAllStringSubmatchIndex(settings, -1) {
		match := []string{settings[index[0]:index[1]], settings[index[2]:index[3]], settings[index[4]:index[5]]}
		// The assignment must end with file(...), as in file("modules").resolve("app") it does not.
		if rest := strings.TrimLeft(settings[index[1]:], " \t"); rest != "" && !strings.ContainsAny(rest[:1], "\r\n;}") {
			return nil, nil, fmt.Errorf("project directories that build logic computes")
		}
		path := ":" + strings.TrimPrefix(match[1], ":")
		if _, known := directories[path]; !known {
			return nil, nil, fmt.Errorf("projectDir is set for %s, which settings do not include", path)
		}
		directories[path] = filepath.Clean(match[2])
	}
	if remaining := projectDirectory.ReplaceAllString(settings, ""); otherInclusion.MatchString(remaining) {
		return nil, nil, fmt.Errorf("project directories that build logic computes")
	}
	return paths, directories, nil
}

// stripComments removes // and /* */ comments outside string literals.
func stripComments(source string) string {
	var out strings.Builder
	var quote byte
	for i := 0; i < len(source); i++ {
		c := source[i]
		switch {
		case quote != 0:
			out.WriteByte(c)
			if c == '\\' && i+1 < len(source) {
				i++
				out.WriteByte(source[i])
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
			out.WriteByte(c)
		case c == '/' && i+1 < len(source) && source[i+1] == '/':
			for i < len(source) && source[i] != '\n' {
				i++
			}
			if i < len(source) {
				out.WriteByte('\n')
			}
		case c == '/' && i+1 < len(source) && source[i+1] == '*':
			end := strings.Index(source[i+2:], "*/")
			if end < 0 {
				return out.String()
			}
			// Keep line numbers stable for statements after the comment.
			out.WriteString(strings.Repeat("\n", strings.Count(source[i:i+2+end+2], "\n")))
			i += 2 + end + 1
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

var (
	// "group:artifact" or "group:artifact:version", in either quote style.
	coordinate = regexp.MustCompile(`["']([A-Za-z0-9_.\-]+):([A-Za-z0-9_.\-]+)(?::[^"'\s]*)?["']`)
	// group: 'g', name: 'a' (Groovy) or group = "g", name = "a" (Kotlin).
	mapCoordinate = regexp.MustCompile(`group\s*[:=]\s*["']([^"']+)["']\s*,\s*name\s*[:=]\s*["']([^"']+)["']`)
)

// buildDependencies lists the coordinates a build file names, directly or
// through the version catalog's libs accessors.
func buildDependencies(build string, catalog map[string][]maven.Dependency) []maven.Dependency {
	var dependencies []maven.Dependency
	for _, match := range coordinate.FindAllStringSubmatch(build, -1) {
		dependencies = append(dependencies, maven.Dependency{Group: match[1], Artifact: match[2]})
	}
	for _, match := range mapCoordinate.FindAllStringSubmatch(build, -1) {
		dependencies = append(dependencies, maven.Dependency{Group: match[1], Artifact: match[2]})
	}
	for _, accessor := range slices.Sorted(func(yield func(string) bool) {
		for key := range catalog {
			if !yield(key) {
				return
			}
		}
	}) {
		if catalogReference(build, accessor) {
			dependencies = append(dependencies, catalog[accessor]...)
		}
	}
	return dependencies
}

// catalogReference reports whether a build file uses a catalog accessor, as
// libs.junit.jupiter, and not only a longer one it prefixes.
func catalogReference(build, accessor string) bool {
	for offset := 0; ; {
		index := strings.Index(build[offset:], accessor)
		if index < 0 {
			return false
		}
		start, end := offset+index, offset+index+len(accessor)
		before := start == 0 || !identifierByte(build[start-1])
		after := end == len(build) || !identifierByte(build[end]) && build[end] != '.'
		if before && after {
			return true
		}
		offset = end
	}
}

func identifierByte(c byte) bool {
	return c == '_' || c == '.' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// catalogFile is the default version catalog, whose accessors start with libs.
const catalogFile = "gradle/libs.versions.toml"

// readCatalog maps each accessor of the build's default version catalog, as
// libs.junit.jupiter or libs.bundles.testing, to the coordinates it names.
func readCatalog(root, build string) (map[string][]maven.Dependency, error) {
	data, err := readInside(root, filepath.Join(build, filepath.FromSlash(catalogFile)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var file struct {
		Libraries map[string]any      `toml:"libraries"`
		Bundles   map[string][]string `toml:"bundles"`
	}
	if err := toml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Join(build, catalogFile), err)
	}
	accessor := func(alias string) string {
		return "libs." + strings.NewReplacer("-", ".", "_", ".").Replace(alias)
	}
	libraries := map[string]maven.Dependency{}
	catalog := map[string][]maven.Dependency{}
	for alias, value := range file.Libraries {
		dependency, ok := catalogLibrary(value)
		if !ok {
			continue
		}
		libraries[alias] = dependency
		catalog[accessor(alias)] = []maven.Dependency{dependency}
	}
	for name, aliases := range file.Bundles {
		var dependencies []maven.Dependency
		for _, alias := range aliases {
			if dependency, ok := libraries[alias]; ok {
				dependencies = append(dependencies, dependency)
			}
		}
		catalog[accessor("bundles."+name)] = dependencies
	}
	return catalog, nil
}

// catalogLibrary reads a [libraries] entry: "group:artifact:version",
// { module = "group:artifact" } or { group = "g", name = "a" }.
func catalogLibrary(value any) (maven.Dependency, bool) {
	module := ""
	switch entry := value.(type) {
	case string:
		module = entry
	case map[string]any:
		if text, ok := entry["module"].(string); ok {
			module = text
		} else {
			group, _ := entry["group"].(string)
			name, _ := entry["name"].(string)
			module = group + ":" + name
		}
	}
	parts := strings.Split(module, ":")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return maven.Dependency{}, false
	}
	return maven.Dependency{Group: parts[0], Artifact: parts[1]}, true
}
