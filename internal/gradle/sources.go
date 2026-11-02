// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gradle

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// TestSourceSet is the source set whose classes the test task task of
// project runs, as its build file declares it literally: the one a Test task
// names in testClassesDirs = sourceSets.<name>.output.classesDirs, the last
// such assignment winning as in Gradle, else test for the test task and a JVM
// test suite's own for the suite's task. A task whose source set ddtest cannot
// follow has none, as when build, the root project, sets testClassesDirs for
// its subprojects.
func TestSourceSet(root string, build, project Project, task string) (string, bool) {
	source, ok := buildSource(root, project)
	if !ok {
		return "", false
	}
	if project.Path != build.Path {
		rootSource, ok := buildSource(root, build)
		if !ok || strings.Contains(rootSource, "testClassesDirs") {
			return "", false
		}
	}
	name := regexp.QuoteMeta(task)
	// The source set is named in the task's declaration or configuration,
	// which may also reconfigure the test task, as tasks.test { … }.
	declaration := regexp.MustCompile(`(?:register|create|maybeCreate)\s*<\s*Test\s*>\s*\(\s*["']` + name + `["'][^{\n]*\{|` +
		`(?:register|create|maybeCreate|named|getByName)\s*\(\s*["']` + name + `["'][^{\n]*\{|` +
		`(?:named|getByName)\s*<\s*Test\s*>\s*\(\s*["']` + name + `["'][^{\n]*\{|` +
		`\btask\s+` + name + `\s*\(\s*type\s*:\s*Test\b[^{\n]*\{|` +
		`\bval\s+` + name + `\s+by\s+(?:tasks\.)?(?:registering|creating|existing|getting)\s*\(\s*Test::class[^{\n]*\{|` +
		`\btasks\.` + name + `\s*\{|(?m:^[ \t]*` + name + `\s*\{)`)
	// Configuration of every Test task, as tasks.withType<Test>() { … },
	// may set this task's classes too.
	for _, match := range typeWideTests.FindAllStringIndex(source, -1) {
		if body, ok := block(source, match[1]-1); !ok || classesAssignment.MatchString(body) {
			return "", false
		}
	}
	// The last assignment decides; one build logic computes, as
	// files(otherClasses), leaves the source set unknown.
	assigned, computed := "", false
	for _, match := range declaration.FindAllStringIndex(source, -1) {
		body, ok := block(source, match[1]-1)
		if !ok {
			continue
		}
		assignments := classesAssignment.FindAllStringIndex(body, -1)
		if assignments == nil {
			continue
		}
		last := body[assignments[len(assignments)-1][0]:]
		if set := classesSourceSet.FindStringSubmatch(last); set != nil && strings.HasPrefix(last, set[0]) {
			assigned, computed = set[1]+set[2]+set[3], false
		} else {
			assigned, computed = "", true
		}
	}
	if computed {
		return "", false
	}
	if assigned != "" {
		return assigned, true
	}
	if task == "test" {
		return "test", true
	}
	if regexp.MustCompile(`\b` + name + `\s*\(\s*JvmTestSuite\s*\)|` +
		`\bval\s+` + name + `\s+by\s+(?:registering|creating|getting)\s*\(\s*JvmTestSuite::class|` +
		`(?:register|create|maybeCreate)\s*<\s*JvmTestSuite\s*>\s*\(\s*["']` + name + `["']|` +
		`(?:register|create|maybeCreate)\s*\(\s*["']` + name + `["']\s*,\s*JvmTestSuite\b`).MatchString(source) {
		return task, true
	}
	return "", false
}

// typeWideTests opens a block that configures every Test task, as
// tasks.withType<Test>().configureEach { or tasks.withType(Test) {.
var typeWideTests = regexp.MustCompile(`withType\s*(?:<\s*Test\s*>|\(\s*Test(?:::class(?:\.java)?)?\s*\))[^{\n]*\{`)

// statement is the rest of one statement: up to a semicolon, brace or line
// end outside string literals.
const statement = `(?:[^;{}\n"']|"[^"\n]*"|'[^'\n]*')*`

// classesAssignment assigns a Test task's testClassesDirs.
var classesAssignment = regexp.MustCompile(`\btestClassesDirs\s*(?:=|\.from\(|\.setFrom\()`)

// classesSourceSet is the source set of testClassesDirs = sourceSets.<name>.output.classesDirs,
// in the spellings sourceSets["name"], sourceSets.getByName("name") and
// sourceSets.named("name").get().
var classesSourceSet = regexp.MustCompile(`testClassesDirs\s*(?:=|\.from\(|\.setFrom\()\s*(?:project\.)?sourceSets` +
	`(?:\.(\w+)|\[\s*["'](\w+)["']\s*\]|\.(?:getByName|named)\(\s*["'](\w+)["']\s*\)(?:\.get\(\))?)\.output\.classesDirs`)

// SourceDirectory is the directory, relative to the project's, of the
// language sources of source set set: src/<set>/<language>, unless the build
// file replaces the set's srcDirs, whose first literal directory it is then;
// as in Gradle, the last replacement in the file wins. srcDirs that build
// logic computes, that leave the project, or that build, the root project,
// replaces for its subprojects, are not followed.
func SourceDirectory(root string, build, project Project, set, language string) (string, bool) {
	directory := filepath.Join("src", set, language)
	source, ok := buildSource(root, project)
	if !ok {
		return "", false
	}
	if project.Path != build.Path {
		rootSource, ok := buildSource(root, build)
		if !ok || srcDirsAssignment.MatchString(rootSource) {
			return "", false
		}
	}
	name, lang := regexp.QuoteMeta(set), regexp.QuoteMeta(language)
	// Each language configuration of the set, at its offset in the file.
	type configuration struct {
		offset int
		body   string
	}
	var configurations []configuration
	// A set's block, as integrationTest(JvmTestSuite) { … } or sourceSets { integrationTest { … } }.
	for _, match := range regexp.MustCompile(`(?:\b`+name+`\b|["']`+name+`["'])[^{}\n]*\{`).FindAllStringIndex(source, -1) {
		if set, ok := block(source, match[1]-1); ok {
			for _, language := range regexp.MustCompile(`\b`+lang+`\s*\{`).FindAllStringIndex(set, -1) {
				if body, ok := block(set, language[1]-1); ok {
					configurations = append(configurations, configuration{match[1] + language[0], body})
				}
			}
			// java.srcDirs = […] inside the set's block.
			for _, assignment := range regexp.MustCompile(`\b`+lang+`\.((?:srcDirs\s*=|setSrcDirs\()`+statement+`)`).FindAllStringSubmatchIndex(set, -1) {
				configurations = append(configurations, configuration{match[1] + assignment[0], set[assignment[2]:assignment[3]]})
			}
		}
	}
	// sourceSets.integrationTest.java.srcDirs = […] on one line.
	for _, match := range regexp.MustCompile(`\b`+name+`\.`+lang+`\.(?:srcDirs\s*=|setSrcDirs\()`+statement).FindAllStringIndex(source, -1) {
		line := source[match[0]:match[1]]
		configurations = append(configurations, configuration{match[0], line[strings.Index(line, lang+".")+len(lang)+1:]})
	}
	slices.SortStableFunc(configurations, func(a, b configuration) int { return a.offset - b.offset })
	for _, configuration := range configurations {
		body := configuration.body
		replacements := srcDirsAssignment.FindAllStringIndex(body, -1)
		if replacements == nil {
			// srcDir adds a directory and keeps the default.
			continue
		}
		// A later replacement may still name a directory ddtest reads.
		literal := firstLiteral.FindStringSubmatch(body[replacements[len(replacements)-1][1]:])
		if literal == nil {
			directory = ""
			continue
		}
		directory = filepath.Clean(literal[1] + literal[2])
		if !filepath.IsLocal(directory) {
			directory = ""
		}
	}
	return directory, directory != ""
}

var (
	// srcDirsAssignment replaces a source directory set's directories.
	srcDirsAssignment = regexp.MustCompile(`\bsrcDirs\s*=|\bsetSrcDirs\s*\(`)
	// firstLiteral is the first directory of the list that follows it, as
	// ['src/itest/java'], listOf("src/itest/java") or files("src/itest/java"),
	// when that element is a whole literal, not the start of a computed path.
	firstLiteral = regexp.MustCompile(`^\s*(?:\[|(?:listOf|setOf|files|mutableListOf)\s*\(|\()?\s*(?:"([^"$]+)"|'([^'$]+)')\s*(?:,|\]|\)|$)`)
)

// buildSource is a project's build file without comments; a project without
// one has an empty source.
func buildSource(root string, project Project) (string, bool) {
	if project.BuildFile == "" {
		return "", true
	}
	data, err := readInside(root, project.BuildFile)
	if err != nil {
		return "", false
	}
	return stripComments(string(data)), true
}

// block is the body of the brace block whose opening brace is at open: the
// text between its braces. Braces inside string literals do not count.
func block(source string, open int) (string, bool) {
	if open < 0 || open >= len(source) || source[open] != '{' {
		return "", false
	}
	depth := 0
	var quote byte
	for i := open; i < len(source); i++ {
		c := source[i]
		switch {
		case quote != 0:
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return source[open+1 : i], true
			}
		}
	}
	return "", false
}
