package testdrive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
)

func currentRubyVersion(root string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "ruby", "--disable=gems", "-e", `print "#{RUBY_ENGINE} #{RUBY_VERSION} #{RUBY_PLATFORM}"`)
	command.Dir = root
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "RUBYOPT=") {
			command.Env = append(command.Env, item)
		}
	}
	command.Env = append(command.Env, "RUBYOPT=")
	data, err := command.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (t *Testdrive) workflowRubyVersion() string {
	if t.rubyVersion != nil {
		return t.rubyVersion()
	}
	return currentRubyVersion(t.repositoryRoot)
}

// Both checks load project Ruby, so use the same owned-copy boundary as tests.
func (t *Testdrive) inRubyWorkflowCopy(ctx context.Context, check func(string) error) (runErr error) {
	parent, err := os.MkdirTemp("", "ddtest-ruby-check-")
	if err != nil {
		return err
	}
	if err := markSessionStorage(parent, t.ownershipRoot()); err != nil {
		_ = os.Remove(parent)
		return err
	}
	defer func() { runErr = errors.Join(runErr, removeMarkedStorage(parent)) }()
	size, err := configurationCopyEstimate(ctx, t.repositoryRoot, true)
	if err != nil {
		return err
	}
	if err := checkConfigurationSpace(parent, size); err != nil {
		return err
	}
	root := filepath.Join(parent, "checkout")
	if err := copyConfiguration(ctx, t.repositoryRoot, root, true); err != nil {
		return err
	}
	return check(root)
}

// Inventory every selected bundle before running any native suite or probe.
func (t *Testdrive) rubyBundleReadiness(ctx context.Context, scope onboard.ValidationScope) ([][]string, error) {
	var missing [][]string
	err := t.inRubyWorkflowCopy(ctx, func(root string) error {
		for _, entry := range scope.Commands {
			if ciInstrumentationDisabled(entry) {
				continue
			}
			env := isolatedCIEnvironment(entry.Environment, t.repositoryRoot, root)
			gemfile, err := rubyConfigurationGemfile(t.repositoryRoot, entry.Directory, env)
			if err != nil {
				return err
			}
			env["BUNDLE_GEMFILE"] = filepath.Join(root, gemfile)
			env["RUBYOPT"] = ""
			_, lockErr := os.Stat(filepath.Join(root, rubyLockfile(gemfile)))
			var checkErr error
			if lockErr == nil {
				executor, ok := t.executor.(directoryExecutor)
				if !ok {
					return errors.New("ruby readiness requires a working-directory command executor")
				}
				_, checkErr = executor.CombinedOutputInDirectory(ctx, filepath.Join(root, entry.Directory), "bundle", []string{"check", "--dry-run"}, env)
			}
			if lockErr != nil || checkErr != nil {
				missing = append(missing, []string{"bundle", "install", "--gemfile", filepath.ToSlash(gemfile)})
			}
		}
		return nil
	})
	return missing, err
}

// Return a checkout-relative manifest while preserving Bundler's cwd semantics.
func rubyConfigurationGemfile(root, directory string, env map[string]string) (string, error) {
	gemfile, explicit := env["BUNDLE_GEMFILE"]
	if !explicit {
		gemfile = os.Getenv("BUNDLE_GEMFILE")
	}
	if gemfile == "" {
		gemfile = "Gemfile"
		if _, err := os.Stat(filepath.Join(root, directory, gemfile)); os.IsNotExist(err) {
			if _, err := os.Stat(filepath.Join(root, directory, "gems.rb")); err == nil {
				gemfile = "gems.rb"
			}
		}
	}
	if !filepath.IsAbs(gemfile) {
		gemfile = filepath.Join(root, directory, gemfile)
	}
	relative, err := filepath.Rel(canonicalWorkflowRoot(root), canonicalWorkflowRoot(gemfile))
	if err != nil || !filepath.IsLocal(relative) {
		return "", fmt.Errorf("BUNDLE_GEMFILE is outside the repository; use a repository-local Gemfile for isolated Ruby validation")
	}
	return relative, nil
}

func rubyLockfile(gemfile string) string {
	if filepath.Base(gemfile) == "gems.rb" {
		return filepath.Join(filepath.Dir(gemfile), "gems.locked")
	}
	return gemfile + ".lock"
}

const rubyPackageFilesScript = `require 'json'; require 'rubygems'
spec = Gem::Specification.load(ARGV.fetch(0))
abort 'gemspec did not load' unless spec
abort 'gemspec returned no packaged files' if spec.files.empty?
puts JSON.generate(spec.files)
`

func (t *Testdrive) rubyPackageExclusions(ctx context.Context) onboard.WorkflowCheck {
	check := onboard.WorkflowCheck{Name: "package_report_exclusions", Status: "passed"}
	specs, err := filepath.Glob(filepath.Join(t.repositoryRoot, "*.gemspec"))
	if err == nil && len(specs) == 0 {
		check.Status, check.Reason = "not applicable", "No root gemspec publishes the root reports."
		return check
	}
	if err == nil {
		err = t.inRubyWorkflowCopy(ctx, func(root string) error {
			executor, ok := t.executor.(directoryExecutor)
			if !ok {
				return errors.New("gemspec inspection requires a working-directory executor")
			}
			// Replace copied metadata (including worktree pointers) with an owned
			// repository. Git-based gemspecs need only the original file index.
			if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
				return err
			}
			if _, err := executor.CombinedOutputInDirectory(ctx, root, "git", []string{"init", "--quiet"}, map[string]string{"GIT_DIR": filepath.Join(root, ".git"), "GIT_WORK_TREE": root}); err != nil {
				return err
			}
			data, err := t.executor.CombinedOutput(ctx, "git", []string{"rev-parse", "--git-path", "index"}, nil)
			if err != nil {
				return err
			}
			index := strings.TrimSpace(string(data))
			if !filepath.IsAbs(index) {
				index = filepath.Join(t.repositoryRoot, index)
			}
			if info, err := os.Stat(index); err == nil {
				if err := copyConfigurationFile(index, filepath.Join(root, ".git", "index"), info); err != nil {
					return err
				}
			}
			for _, report := range []string{"testdrive.json", "report.html"} {
				path := filepath.Join(root, ".testoptimization", report)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					return err
				}
				if err := os.WriteFile(path, []byte("report exclusion check"), 0600); err != nil {
					return err
				}
			}
			for _, spec := range specs {
				data, err := executor.CombinedOutputInDirectory(ctx, root, "ruby", []string{"-e", rubyPackageFilesScript, filepath.Base(spec)}, map[string]string{"RUBYOPT": "", "GIT_DIR": filepath.Join(root, ".git"), "GIT_WORK_TREE": root})
				if err != nil {
					return fmt.Errorf("inspect %s packaging: %w: %s", filepath.Base(spec), err, commandDiagnostic(data))
				}
				var files []string
				if err := json.Unmarshal(data, &files); err != nil {
					return fmt.Errorf("gemspec did not produce a file inventory: %w", err)
				}
				for _, file := range files {
					if filepath.IsAbs(file) {
						relative, err := filepath.Rel(root, file)
						if err != nil || !filepath.IsLocal(relative) {
							return fmt.Errorf("gemspec packages a file outside the repository")
						}
						file = relative
					}
					file = filepath.ToSlash(filepath.Clean(file))
					if !filepath.IsLocal(filepath.FromSlash(file)) {
						return fmt.Errorf("gemspec packages a non-local file")
					}
					if file == ".testoptimization" || strings.HasPrefix(file, ".testoptimization/") {
						return fmt.Errorf("%s includes local validation reports; exclude .testoptimization from its files", filepath.Base(spec))
					}
				}
			}
			return nil
		})
	}
	if err != nil {
		check.Status, check.Reason = "failed", reportText(err.Error())
	} else {
		check.Reason = "Root gemspec inventories exclude both reports; loaded only in an owned copy, without building or publishing a gem."
	}
	return check
}
