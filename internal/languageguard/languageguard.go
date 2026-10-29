// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package languageguard checks that language-specific code stays behind a
// package's language seam.
//
// A package with a seam keeps each language in files named after it
// (python*.go, ruby*.go, java_*.go), registers every language in languages.go, and leaves
// the rest of its files to shared orchestration and JavaScript, the language
// they were written for. Check reads the identifiers and string literals of
// every non-test file, ignoring comments, and fails when:
//   - a shared or JavaScript file names another language; or
//   - a language file names a different language, JavaScript included.
//
// languages.go is the only file allowed to name every language.
package languageguard

import (
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// RegistryFile is the one file of a package that may name every language.
const RegistryFile = "languages.go"

// language owns the files named with its prefix and the tokens its markers match.
type language struct {
	name   string
	prefix string
	marker *regexp.Regexp
	// shared are terms the language shares with another, such as a framework
	// both have; its own files may name them.
	shared *regexp.Regexp
}

// languages lists the markers that identify each language's code. JavaScript
// owns the shared files, so it has no prefix; its markers only keep it out of
// other languages' files.
var languages = []language{
	{name: "javascript", marker: regexp.MustCompile(`(?i)javascript|dd-trace|node_options|jest|vitest|mocha|cypress|playwright|cucumber|webdriverio`)},
	{name: "python", prefix: "python", marker: regexp.MustCompile(`(?i)python|pytest|ddtrace`)},
	{name: "ruby", prefix: "ruby", marker: regexp.MustCompile(`(?i)ruby|rspec|minitest|gemfile|gems\.rb|bundle (exec|install|config)|bundle_|datadog-ci`)},
	// "java" is a prefix of "javascript": Java files are java_*.go, and its
	// markers are whole words or Java-only terms. JUnit XML is a report format
	// every language uses, so only the JVM framework names count.
	// Cucumber JVM shares its name with Cucumber.js.
	{name: "java", prefix: "java_", marker: regexp.MustCompile(`(?i)\bjava\b|maven|\bmvnw?\b|junit[45]|jupiter|testng|surefire|failsafe|dd-java-agent|pom\.xml`), shared: regexp.MustCompile(`(?i)cucumber`)},
}

// Check fails t for every token in dir's non-test Go files that names a
// language outside the file's own.
func Check(t testing.TB, dir string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		name := filepath.Base(path)
		if strings.HasSuffix(name, "_test.go") || name == RegistryFile {
			continue
		}
		owner := ownerOf(name)
		own := languages[slices.IndexFunc(languages, func(language language) bool { return language.name == owner })]
		scan(t, path, func(literal string, position token.Position) {
			if own.shared != nil {
				literal = own.shared.ReplaceAllString(literal, "")
			}
			for _, other := range languages {
				if other.name == owner || owner == "javascript" && other.name == "javascript" {
					continue
				}
				if other.marker.MatchString(literal) {
					t.Errorf("%s: %s code names %s (%s); move it into %s*.go behind the language seam", position, owner, other.name, literal, other.name)
				}
			}
		})
	}
}

// ownerOf returns the language whose prefix names the file; shared files belong
// to JavaScript.
func ownerOf(name string) string {
	index := slices.IndexFunc(languages, func(language language) bool {
		return language.prefix != "" && strings.HasPrefix(name, language.prefix)
	})
	if index < 0 {
		return "javascript"
	}
	return languages[index].name
}

// CheckNeutral fails t for every token in dir's non-test Go files matching
// patterns that names any language, JavaScript included. It keeps files that
// belong to another seam, such as CI providers, free of language decisions.
func CheckNeutral(t testing.TB, dir string, patterns ...string) {
	t.Helper()
	for _, pattern := range patterns {
		files, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range files {
			name := filepath.Base(path)
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			scan(t, path, func(literal string, position token.Position) {
				for _, language := range languages {
					if language.marker.MatchString(literal) {
						t.Errorf("%s: names %s (%s); move language decisions behind the language seam", position, language.name, literal)
					}
				}
			})
		}
	}
}

// scan calls found with every identifier and string literal of path, ignoring comments.
func scan(t testing.TB, path string, found func(literal string, position token.Position)) {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fileSet := token.NewFileSet()
	file := fileSet.AddFile(filepath.Base(path), fileSet.Base(), len(source))
	var tokens scanner.Scanner
	tokens.Init(file, source, nil, 0)
	for {
		position, kind, literal := tokens.Scan()
		if kind == token.EOF {
			return
		}
		if kind == token.IDENT || kind == token.STRING {
			found(literal, fileSet.Position(position))
		}
	}
}
