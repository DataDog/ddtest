package compatibility

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/DataDog/ddtest/internal/utils"
	"github.com/spf13/viper"
)

func requireEnv(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Skipf("%s is not set", name)
	}
	return value
}

func writeFixture(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}

func requireFiles(t *testing.T, got, want []string) {
	t.Helper()
	normalized := slices.Clone(got)
	for i := range normalized {
		normalized[i] = utils.NormalizePath(normalized[i])
	}
	slices.Sort(normalized)

	expected := slices.Clone(want)
	for i := range expected {
		expected[i] = utils.NormalizePath(expected[i])
	}
	slices.Sort(expected)

	if !slices.Equal(normalized, expected) {
		t.Fatalf("files = %v, want %v", normalized, expected)
	}
}

func requireTestSources(t *testing.T, tests []testoptimization.Test, want []string) {
	t.Helper()
	if len(tests) != len(want) {
		t.Fatalf("discovered %d tests, want %d: %+v", len(tests), len(want), tests)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	sources := make([]string, 0, len(tests))
	for _, test := range tests {
		if strings.TrimSpace(test.Name) == "" {
			t.Fatalf("discovered test has no name: %+v", test)
		}
		source := filepath.FromSlash(test.SuiteSourceFile)
		if filepath.IsAbs(source) {
			if relative, relativeErr := filepath.Rel(cwd, source); relativeErr == nil {
				source = relative
			}
		}
		sources = append(sources, utils.NormalizePath(source))
	}
	requireFiles(t, sources, want)
}

func resetSettingsAfterTest(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		viper.Reset()
		settings.Init()
	})
}

func configureFramework(command, testsLocation string) {
	viper.Reset()
	if command != "" {
		viper.Set("command", command)
	}
	if testsLocation != "" {
		viper.Set("tests_location", testsLocation)
	}
	settings.Init()
}

func shellCommand(parts ...string) string {
	quoted := make([]string, len(parts))
	for i, part := range parts {
		quoted[i] = strconv.Quote(part)
	}
	return strings.Join(quoted, " ")
}
