package framework

import (
	"slices"
	"strings"
	"testing"
)

func TestFrameworkArgumentBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		args, options, files, want []string
	}{
		{"direct", []string{"--grep", "smoke", "--", "old.test.js"}, []string{"--listTests"}, nil, []string{"--grep", "smoke", "--listTests", "--", "old.test.js"}},
		{"replace selected files", []string{"--grep", "smoke", "--", "old.test.js"}, []string{"--runTestsByPath"}, []string{"selected.test.js"}, []string{"--grep", "smoke", "--runTestsByPath", "--", "selected.test.js"}},
		{"wrapper separator", []string{"--", "jest", "--runInBand"}, []string{"--listTests"}, nil, []string{"--", "jest", "--runInBand", "--listTests"}},
		{"both separators", []string{"--", "jest", "--runInBand", "--", "old.js"}, []string{"--runTestsByPath"}, []string{"new.js"}, []string{"--", "jest", "--runInBand", "--runTestsByPath", "--", "new.js"}},
		{"without separator unchanged", []string{"jest", "--runInBand"}, []string{"--runTestsByPath"}, []string{"new.js"}, []string{"jest", "--runInBand", "--runTestsByPath", "new.js"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := slices.Clone(tc.args)
			command := "jest"
			if strings.Contains(tc.name, "wrapper") || tc.name == "both separators" || tc.name == "without separator unchanged" {
				command = "npx"
			}
			got := WithFrameworkOptions(command, tc.args, "jest", tc.options...)
			if tc.files != nil {
				got = withFrameworkFiles(command, got, "jest", tc.files)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if !slices.Equal(tc.args, original) {
				t.Fatal("mutated command")
			}
		})
	}
}

func TestRubyAndPythonSelectedFileArguments(t *testing.T) {
	var got []string
	executor := &jestCommandExecutor{onExecution: func(_ string, args []string) { got = slices.Clone(args) }}
	for _, tc := range []struct {
		runner Framework
		want   []string
	}{
		{&RSpec{platform: &testPlatform{}, executor: executor, commandOverride: []string{"bundle", "exec", "rspec", "--tag", "smoke", "--", "old.rb"}}, []string{"exec", "rspec", "--tag", "smoke", "--format", "progress", "--", "selected"}},
		{&PyTest{platform: &testPlatform{}, executor: executor, commandOverride: []string{"python", "-m", "pytest", "-k", "smoke", "--", "old.py"}}, []string{"-m", "pytest", "-k", "smoke", "--", "selected"}},
	} {
		if err := tc.runner.RunTests(t.Context(), []string{"selected"}, nil); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, tc.want) {
			t.Fatalf("%s got %q, want %q", tc.runner.Name(), got, tc.want)
		}
	}
}

func TestNpmFrameworkOptionsUseBothForwardingBoundaries(t *testing.T) {
	for _, tc := range []struct{ args, want []string }{
		{[]string{"test"}, []string{"test", "--", "--showConfig"}},
		{[]string{"run", "test", "--", "--config", "jest.cjs"}, []string{"run", "test", "--", "--config", "jest.cjs", "--showConfig"}},
		{[]string{"test", "--", "--", "file.js"}, []string{"test", "--", "--showConfig", "--", "file.js"}},
	} {
		got := WithFrameworkOptions("npm", tc.args, "jest", "--showConfig")
		if !slices.Equal(got, tc.want) {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
	}
}

func TestNpmSelectedFilesKeepGeneratedRunnerOptions(t *testing.T) {
	for _, args := range [][]string{{"test"}, {"run", "test"}, {"test", "--"}, {"test", "--", "--", "old.test.js"}} {
		got := WithFrameworkOptions("npm", args, "jest", "--runTestsByPath")
		got = withFrameworkFiles("npm", got, "jest", []string{"selected[shard].test.js"})
		prefix := []string{"test", "--", "--runTestsByPath"}
		if args[0] == "run" {
			prefix = []string{"run", "test", "--", "--runTestsByPath"}
		}
		if len(args) > 2 && args[2] == "--" {
			prefix = append(prefix, "--")
		}
		want := append(prefix, "selected[shard].test.js")
		if !slices.Equal(got, want) {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}
