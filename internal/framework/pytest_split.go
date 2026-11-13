package framework

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
)

// pytestSplitFilterPlugin filters the tests of split files; see the script.
//
//go:embed scripts/pytest_split_filter.py
var pytestSplitFilterPlugin string

const (
	splitFilterModule = "ddtest_split_filter"
	splitFilterEnvVar = "DDTEST_SPLIT_FILTER"
)

// splitFileSelection is how pytest runs a batch's lines when some are parts of
// files the plan split by test (see constants.SplitFileTestsPath).
type splitFileSelection struct {
	// args are the batch's lines with each split file once, as a path.
	args []string
	// filters maps a split file's absolute path to the tests of it the run
	// keeps ("keep") or, for the last part of the file, drops ("drop").
	filters map[string]map[string][]string
	// onlySplitFiles says whether every line is a part of a split file.
	onlySplitFiles bool
}

// selectSplitFileTests passes pytest each split file of the batch as a path
// and filters its tests with the plugin, instead of passing test IDs, which
// fail the whole run when a test no longer exists. A file's last part, its
// path among the lines, keeps the tests of the file minus those of the
// file's other parts, except those in the same batch; the other parts keep
// their tests. Without split files, it leaves the lines alone.
func selectSplitFileTests(lines []string) (splitFileSelection, error) {
	listed, err := readSplitFileTests()
	if err != nil || len(listed) == 0 {
		return splitFileSelection{args: lines}, err
	}
	selection := splitFileSelection{filters: map[string]map[string][]string{}, onlySplitFiles: true}
	kept := map[string][]string{}
	var lastParts []string
	for _, line := range lines {
		file, _, isTestID := strings.Cut(line, "::")
		if _, split := listed[file]; !split {
			selection.args = append(selection.args, line)
			selection.onlySplitFiles = false
			continue
		}
		if !slices.Contains(selection.args, file) {
			selection.args = append(selection.args, file)
		}
		if isTestID {
			kept[file] = append(kept[file], line)
		} else {
			lastParts = append(lastParts, file)
		}
	}
	for file, ids := range kept {
		if !slices.Contains(lastParts, file) {
			if err := selection.addFilter(file, "keep", ids); err != nil {
				return splitFileSelection{}, err
			}
		}
	}
	for _, file := range lastParts {
		drop := slices.DeleteFunc(slices.Clone(listed[file]), func(id string) bool {
			return slices.Contains(kept[file], id)
		})
		if err := selection.addFilter(file, "drop", drop); err != nil {
			return splitFileSelection{}, err
		}
	}
	return selection, nil
}

func (s splitFileSelection) addFilter(file string, rule string, ids []string) error {
	absolute, err := filepath.Abs(file)
	if err != nil {
		return fmt.Errorf("resolve split test file %s: %w", file, err)
	}
	s.filters[absolute] = map[string][]string{rule: append([]string{}, ids...)}
	return nil
}

// readSplitFileTests reads the plan's tests of split files, nil without one.
func readSplitFileTests() (map[string][]string, error) {
	data, err := os.ReadFile(constants.SplitFileTestsPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read split file tests: %w", err)
	}
	var listed map[string][]string
	if err := json.Unmarshal(data, &listed); err != nil {
		return nil, fmt.Errorf("parse split file tests %s: %w", constants.SplitFileTestsPath, err)
	}
	return listed, nil
}

// writeSplitFilter writes the plugin and the filters to a temporary directory
// and makes pytest load the plugin through env, the run's environment. It
// returns the filters' path and a function that removes the directory.
func writeSplitFilter(filters map[string]map[string][]string, env map[string]string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "ddtest-pytest-split-*")
	if err != nil {
		return "", nil, fmt.Errorf("create split filter directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	data, err := json.Marshal(filters)
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, "filters.json"), data, 0o600)
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, splitFilterModule+".py"), []byte(pytestSplitFilterPlugin), 0o600)
	}
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("write split filter: %w", err)
	}
	env[splitFilterEnvVar] = filepath.Join(dir, "filters.json")
	env["PYTHONPATH"] = prependEnvList(env, "PYTHONPATH", string(os.PathListSeparator), dir)
	// PYTEST_PLUGINS, unlike -p, reaches pytest through wrappers that pass
	// their own arguments, such as a command override's script.
	env["PYTEST_PLUGINS"] = prependEnvList(env, "PYTEST_PLUGINS", ",", splitFilterModule)
	return env[splitFilterEnvVar], cleanup, nil
}

// prependEnvList puts value first in the list variable key of env, or of the
// process environment when env does not set it; a variable env unsets starts
// empty.
func prependEnvList(env map[string]string, key string, separator string, value string) string {
	current, ok := env[key]
	if !ok {
		current = os.Getenv(key)
	}
	if current == "" || current == ext.UnsetEnvironment {
		return value
	}
	return value + separator + current
}
