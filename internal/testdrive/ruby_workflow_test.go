package testdrive

import (
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

type rubyReadinessExecutor struct {
	directories  []string
	environments []map[string]string
}

func (e *rubyReadinessExecutor) CombinedOutput(context.Context, string, []string, map[string]string) ([]byte, error) {
	return nil, nil
}
func (e *rubyReadinessExecutor) CombinedOutputInDirectory(_ context.Context, root, name string, args []string, env map[string]string) ([]byte, error) {
	e.directories = append(e.directories, root)
	e.environments = append(e.environments, maps.Clone(env))
	return []byte("dependencies satisfied"), nil
}
func TestRubyReadinessInventoriesMissingBundlesWithoutChangingLocks(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "Gemfile"), "source 'https://rubygems.org'\n")
	requireWriteFile(t, filepath.Join(root, "Gemfile.lock"), "installed lock\n")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gemfiles"), 0700))
	requireWriteFile(t, filepath.Join(root, "gemfiles", "rails.gemfile"), "source 'https://rubygems.org'\n")
	executor := &rubyReadinessExecutor{}
	drive := &Testdrive{repositoryRoot: root, executor: executor}
	scope := onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: "."}, {Directory: ".", Environment: map[string]string{"BUNDLE_GEMFILE": "gemfiles/rails.gemfile"}}}}
	commands, err := drive.rubyBundleReadiness(t.Context(), scope)
	require.NoError(t, err)
	require.Equal(t, [][]string{{"bundle", "install", "--gemfile", "gemfiles/rails.gemfile"}}, commands)
	require.Len(t, executor.directories, 1)
	require.NotEqual(t, root, executor.directories[0])
	require.NoDirExists(t, executor.directories[0])
	require.Contains(t, executor.environments[0]["BUNDLE_GEMFILE"], "checkout")
	require.NoFileExists(t, filepath.Join(root, "gemfiles/rails.gemfile.lock"))
	data, err := os.ReadFile(filepath.Join(root, "Gemfile.lock"))
	require.NoError(t, err)
	require.Equal(t, "installed lock\n", string(data))
}
func TestRubyGemPackagingUsesCopyAndChecksReportMembership(t *testing.T) {
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("Ruby is required")
	}
	for _, glob := range []string{"lib/**/*", "{lib,.testoptimization}/**/*", "git-index"} {
		t.Run(glob, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, "lib"), 0700))
			requireWriteFile(t, filepath.Join(root, "lib/fixture.rb"), "module Fixture;end\n")
			command := exec.CommandContext(t.Context(), "git", "init", "--quiet", root)
			data, err := command.CombinedOutput()
			require.NoError(t, err, string(data))
			filesExpression := "Dir['" + glob + "']"
			if glob == "git-index" {
				filesExpression = "`git ls-files -z`.split(\"\\x00\")"
			}
			requireWriteFile(t, filepath.Join(root, "fixture.gemspec"), "Gem::Specification.new do |s|\n s.name='fixture';s.version='1.0';s.files="+filesExpression+";File.write('gemspec-loaded.txt',Dir.pwd)\nend\n")
			// Real clones have an index; a brand-new git init alone misses this case.
			data, err = exec.CommandContext(t.Context(), "git", "-C", root, "add", "lib/fixture.rb", "fixture.gemspec").CombinedOutput()
			require.NoError(t, err, string(data))
			indexPath := filepath.Join(root, ".git/index")
			if glob == "git-index" {
				// Linked worktrees copy a .git file pointing outside the checkout.
				metadata := filepath.Join(t.TempDir(), "metadata")
				require.NoError(t, os.Rename(filepath.Join(root, ".git"), metadata))
				requireWriteFile(t, filepath.Join(root, ".git"), "gitdir: "+metadata+"\n")
				indexPath = filepath.Join(metadata, "index")
			}
			indexBefore, err := os.ReadFile(indexPath)
			require.NoError(t, err)
			drive := &Testdrive{repositoryRoot: root, language: "ruby", executor: &ext.DefaultCommandExecutor{}}
			// The real executor uses the command's explicit owned working directory.
			t.Chdir(root)
			result := drive.rubyPackageExclusions(t.Context())
			if glob != "{lib,.testoptimization}/**/*" {
				require.Equal(t, "passed", result.Status, result.Reason)
			} else {
				require.Equal(t, "failed", result.Status, result.Reason)
				require.Contains(t, result.Reason, "includes local validation reports")
			}
			indexAfter, err := os.ReadFile(indexPath)
			require.NoError(t, err)
			require.Equal(t, indexBefore, indexAfter, "the original Git index must be unchanged")
			require.NoFileExists(t, filepath.Join(root, "gemspec-loaded.txt"))
			require.NoDirExists(t, filepath.Join(root, ".testoptimization"))
		})
	}
}
