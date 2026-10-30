// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gradle

import (
	"path/filepath"
	"regexp"
	"strings"
)

// TestSourceSet is the source set whose classes the test task task of
// project runs, as its build file declares it literally: test for the test
// task, a JVM test suite's own for the suite's task, or the one a Test task
// names in testClassesDirs = sourceSets.<name>.output.classesDirs. A task
// whose source set ddtest cannot follow has none.
func TestSourceSet(root string, project Project, task string) (string, bool) {
	if task == "test" {
		return "test", true
	}
	source, ok := buildSource(root, project)
	if !ok {
		return "", false
	}
	name := regexp.QuoteMeta(task)
	if regexp.MustCompile(`\b` + name + `\s*\(\s*JvmTestSuite\s*\)|` +
		`\bval\s+` + name + `\s+by\s+(?:registering|creating|getting)\s*\(\s*JvmTestSuite::class|` +
		`(?:register|create|maybeCreate)\s*<\s*JvmTestSuite\s*>\s*\(\s*["']` + name + `["']|` +
		`(?:register|create|maybeCreate)\s*\(\s*["']` + name + `["']\s*,\s*JvmTestSuite\b`).MatchString(source) {
		return task, true
	}
	// The source set is named in the task's declaration or configuration.
	declaration := regexp.MustCompile(`(?:register|create|maybeCreate)\s*<\s*Test\s*>\s*\(\s*["']` + name + `["'][^{\n]*\{|` +
		`(?:register|create|maybeCreate|named)\s*\(\s*["']` + name + `["'][^{\n]*\{|` +
		`(?:named|getByName)\s*<\s*Test\s*>\s*\(\s*["']` + name + `["'][^{\n]*\{|` +
		`\btask\s+` + name + `\s*\(\s*type\s*:\s*Test\b[^{\n]*\{|` +
		`\bval\s+` + name + `\s+by\s+(?:tasks\.)?(?:registering|creating|existing)\s*\(\s*Test::class[^{\n]*\{`)
	for _, match := range declaration.FindAllStringIndex(source, -1) {
		body, ok := block(source, match[1]-1)
		if !ok {
			continue
		}
		if set := classesSourceSet.FindStringSubmatch(body); set != nil {
			return set[1] + set[2] + set[3], true
		}
	}
	return "", false
}

// classesSourceSet is the source set of testClassesDirs = sourceSets.<name>.output.classesDirs,
// in the spellings sourceSets["name"], sourceSets.getByName("name") and
// sourceSets.named("name").get().
var classesSourceSet = regexp.MustCompile(`testClassesDirs\s*(?:=|\.from\(|\.setFrom\()\s*(?:project\.)?sourceSets` +
	`(?:\.(\w+)|\[\s*["'](\w+)["']\s*\]|\.(?:getByName|named)\(\s*["'](\w+)["']\s*\)(?:\.get\(\))?)\.output\.classesDirs`)

// SourceDirectory is the directory, relative to the project's, of the
// language sources of source set set: src/<set>/<language>, unless the build
// file replaces the set's srcDirs, whose first literal directory it is then.
// srcDirs that build logic computes, or that leave the project, are not
// followed.
func SourceDirectory(root string, project Project, set, language string) (string, bool) {
	directory := filepath.Join("src", set, language)
	source, ok := buildSource(root, project)
	if !ok {
		return "", false
	}
	name, lang := regexp.QuoteMeta(set), regexp.QuoteMeta(language)
	var bodies []string
	// A set's block, as integrationTest(JvmTestSuite) { … } or sourceSets { integrationTest { … } }.
	for _, match := range regexp.MustCompile(`(?:\b`+name+`\b|["']`+name+`["'])[^{}\n]*\{`).FindAllStringIndex(source, -1) {
		if set, ok := block(source, match[1]-1); ok {
			for _, language := range regexp.MustCompile(`\b`+lang+`\s*\{`).FindAllStringIndex(set, -1) {
				if body, ok := block(set, language[1]-1); ok {
					bodies = append(bodies, body)
				}
			}
		}
	}
	// sourceSets.integrationTest.java.srcDirs = […] on one line.
	for _, match := range regexp.MustCompile(`\b`+name+`\.`+lang+`\.(?:srcDirs\s*=|setSrcDirs\()[^\n]*`).FindAllString(source, -1) {
		bodies = append(bodies, match[strings.Index(match, lang+".")+len(lang)+1:])
	}
	for _, body := range bodies {
		replaced := srcDirsAssignment.FindStringIndex(body)
		if replaced == nil {
			// srcDir adds a directory and keeps the default.
			continue
		}
		literal := firstLiteral.FindStringSubmatch(body[replaced[1]:])
		if literal == nil {
			return "", false
		}
		directory = filepath.Clean(literal[1] + literal[2])
		if !filepath.IsLocal(directory) {
			return "", false
		}
	}
	return directory, true
}

var (
	// srcDirsAssignment replaces a source directory set's directories.
	srcDirsAssignment = regexp.MustCompile(`\bsrcDirs\s*=|\bsetSrcDirs\s*\(`)
	// firstLiteral is the first directory of the list that follows it, as
	// ['src/itest/java'], listOf("src/itest/java") or files("src/itest/java").
	firstLiteral = regexp.MustCompile(`^\s*(?:\[|(?:listOf|setOf|files|mutableListOf)\s*\(|\()?\s*(?:"([^"$]+)"|'([^'$]+)')`)
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
