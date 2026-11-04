package onboard

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
)

var istanbulPlugin = regexp.MustCompile(`['"](?:babel-plugin-)?istanbul['"]`)

// This is static configuration evidence, not proof that Babel instrumented the
// executed source. Real-suite coverage must still be observed by testdrive.
func MochaBabelIstanbulConfigured(root string) bool {
	for _, name := range []string{".babelrc", ".babelrc.json", ".babelrc.js", ".babelrc.cjs", "babel.config.js", "babel.config.cjs", "babel.config.json"} {
		path := filepath.Join(root, name)
		if info, err := os.Stat(path); err == nil && info.Size() <= 1<<20 {
			if data, err := os.ReadFile(path); err == nil && istanbulPlugin.Match(data) {
				return true
			}
		}
	}
	var manifest struct{ Babel json.RawMessage }
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	return err == nil && json.Unmarshal(data, &manifest) == nil && istanbulPlugin.Match(manifest.Babel)
}

// mochaCoverageSettled reports a Mocha command that needs no nyc wrapper for
// source coverage: it runs under nyc, Babel's Istanbul plugin instruments its
// source, or its package has no source nyc would instrument.
func (c TestCommand) mochaCoverageSettled() bool {
	return c.usesNYC || c.mochaIstanbul || c.mochaNoSource
}

// MochaCoverageNotApplicable reports a Mocha command, run in directory of
// the checkout at root, whose package has no file nyc would instrument, so no
// real-suite source coverage can be observed whatever the setup.
func MochaCoverageNotApplicable(root, directory, command string) bool {
	remaining := &scriptBudget{left: packageScriptExpansionLimit}
	result := resolveJestSequence(root, directory, command, nil, remaining, "mocha")
	return result.Reason == "" && len(result.Tests) == 1 && result.Tests[0].mochaNoSource
}

// nycDefaultExclusions are the paths, relative to nyc's working directory,
// that nyc leaves out unless its configuration says otherwise: node_modules
// and @istanbuljs/schema's default exclude list. nycDefaultExtensions are the
// file extensions it instruments.
var (
	nycDefaultExtensions = []string{".js", ".cjs", ".mjs", ".ts", ".tsx", ".jsx"}
	nycDefaultExclusions = []*regexp.Regexp{
		regexp.MustCompile(`(?:^|/)node_modules/`),
		regexp.MustCompile(`^coverage/`),
		regexp.MustCompile(`^packages/[^/]+/tests?/`),
		regexp.MustCompile(`\.d\.ts$`),
		regexp.MustCompile(`^tests?/`),
		regexp.MustCompile(`^test(?:-[^/]*)?\.(?:js|cjs|mjs|ts|tsx|jsx)$`),
		regexp.MustCompile(`(?:^|/)[^/]*[.-]test\.(?:js|cjs|mjs|ts|tsx|jsx)$`),
		regexp.MustCompile(`(?:^|/)__tests__/`),
		regexp.MustCompile(`(?:^|/)(?:ava|babel|nyc)\.config\.(?:js|cjs|mjs)$`),
		regexp.MustCompile(`(?:^|/)jest\.config\.(?:js|cjs|mjs|ts)$`),
		regexp.MustCompile(`(?:^|/)(?:karma|rollup|webpack)\.config\.js$`),
		regexp.MustCompile(`(?:^|/)\.(?:eslint|mocha)rc\.(?:js|cjs)$`),
	}
	nycConfigurations = []string{".nycrc", ".nycrc.json", ".nycrc.yml", ".nycrc.yaml", "nyc.config.js", "nyc.config.cjs", "nyc.config.mjs"}
)

// mochaInstrumentableSource reports whether nyc, run in directory (a package
// below root, or root), could instrument any file: one with an extension nyc
// instruments, below directory and outside its default exclusions. A test
// that loads only test files and data, such as a suite validating a JSON
// schema, reports no source coverage whatever its setup, so a coverage check
// can't apply to it. It is true whenever ddtest can't tell: an nyc
// configuration in the package or above it (which may include test files or
// other extensions), a folder it can't read or one too large to list, or one
// without even a test file nyc leaves out, whose tests ddtest doesn't see.
func mochaInstrumentableSource(root, directory string) bool {
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return true
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return true
	}
	if relative, err := filepath.Rel(canonicalRoot, canonical); err != nil || !filepath.IsLocal(relative) && relative != "." {
		return true
	}
	// nyc finds its configuration in its working directory or above it.
	for current := canonical; ; current = filepath.Dir(current) {
		for _, name := range nycConfigurations {
			if _, err := os.Lstat(filepath.Join(current, name)); !os.IsNotExist(err) {
				return true
			}
		}
		var manifest map[string]json.RawMessage
		if data, err := os.ReadFile(filepath.Join(current, "package.json")); err == nil {
			if json.Unmarshal(data, &manifest) != nil {
				return true
			}
			if _, configured := manifest["nyc"]; configured {
				return true
			}
		} else if !os.IsNotExist(err) {
			return true
		}
		if current == canonicalRoot || filepath.Dir(current) == current {
			break
		}
	}
	found, excluded, listed := false, false, 0
	err = filepath.WalkDir(canonical, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if listed++; listed > 20000 {
			return errors.New("too many files")
		}
		relative, _ := filepath.Rel(canonical, path)
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !slices.Contains(nycDefaultExtensions, filepath.Ext(entry.Name())) {
			return nil
		}
		if !slices.ContainsFunc(nycDefaultExclusions, func(exclusion *regexp.Regexp) bool { return exclusion.MatchString(relative) }) {
			found = true
			return filepath.SkipAll
		}
		excluded = true
		return nil
	})
	return found || !excluded || err != nil
}
