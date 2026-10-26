package testdrive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/kballard/go-shellquote"
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
puts JSON.generate(spec.files)
`

func (t *Testdrive) rubyPackageExclusions(ctx context.Context) onboard.WorkflowCheck {
	check := onboard.WorkflowCheck{Name: "package_report_exclusions", Status: "passed"}
	var specs []string
	err := filepath.WalkDir(t.repositoryRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(t.repositoryRoot, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// Dependency/configuration trees and test fixtures aren't publish targets.
			if relative != "." && (slices.Contains([]string{".git", ".bundle", "node_modules", ".testoptimization", "test", "tests", "spec", "fixtures"}, entry.Name()) || filepath.ToSlash(relative) == "vendor/bundle") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".gemspec") {
			if !entry.Type().IsRegular() {
				return fmt.Errorf("gemspec is not a regular repository file: %s", relative)
			}
			specs = append(specs, relative)
		}
		return nil
	})
	if err == nil && len(specs) == 0 {
		check.Status, check.Reason = "not applicable", "No repository gemspec publishes the root reports."
		return check
	}
	if err == nil {
		err = t.inRubyWorkflowCopy(ctx, func(root string) error {
			root, err = filepath.EvalSymlinks(root)
			if err != nil {
				return err
			}
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
				// A nested gem may be built from its own directory or the checkout.
				// Inspect every loadable, nonempty inventory in those contexts.
				valid := false
				var loadErr error
				for _, directory := range slices.Compact([]string{root, filepath.Join(root, filepath.Dir(spec))}) {
					data, err := executor.CombinedOutputInDirectory(ctx, directory, "ruby", []string{"-e", rubyPackageFilesScript, filepath.Join(root, spec)}, map[string]string{"RUBYOPT": "", "GIT_DIR": filepath.Join(root, ".git"), "GIT_WORK_TREE": root})
					if err != nil {
						if ctx.Err() != nil {
							return ctx.Err()
						}
						loadErr = fmt.Errorf("inspect %s packaging: %w", spec, err)
						continue
					}
					var files []string
					if err := json.Unmarshal(data, &files); err != nil {
						return fmt.Errorf("%s did not produce a file inventory: %w", spec, err)
					}
					valid = valid || len(files) > 0
					for _, file := range files {
						if !filepath.IsAbs(file) {
							file = filepath.Join(directory, file)
						}
						relative, err := filepath.Rel(root, file)
						if err != nil || !filepath.IsLocal(relative) {
							return fmt.Errorf("%s packages a file outside the repository", spec)
						}
						file = filepath.ToSlash(filepath.Clean(relative))
						if file == ".testoptimization" || strings.HasPrefix(file, ".testoptimization/") {
							return fmt.Errorf("%s includes local validation reports; exclude .testoptimization from its files", spec)
						}
					}
				}
				if !valid {
					if loadErr != nil {
						return loadErr
					}
					return fmt.Errorf("%s returned no packaged files", spec)
				}
			}
			return nil
		})
	}
	if err != nil {
		check.Status, check.Reason = "failed", reportText(err.Error())
	} else {
		check.Reason = "Repository gemspec inventories exclude both reports; loaded only in an owned copy, without building or publishing a gem."
	}
	return check
}

// Bind one bundle's metadata only to its discovered CI consumers. Focused
// commands can claim matching locations, never unrelated bundles or commands.
func (t *Testdrive) rubyRuntimeLocations() ([]string, error) {
	if len(t.rubyCILocations) > 0 {
		return t.rubyCILocations, nil
	}
	root := t.checkoutRoot()
	directory, err := filepath.Rel(root, t.repositoryRoot)
	if err != nil {
		return nil, err
	}
	gemfile, err := rubyConfigurationGemfile(root, directory, t.configurationEnvironment)
	if err != nil {
		return nil, err
	}
	scope, err := onboard.DiscoverValidationScope(root, t.framework.Name())
	if err != nil {
		return nil, err
	}
	command := shellquote.Join(append([]string{t.command}, t.args...)...)
	var locations []string
	for _, entry := range scope.Commands {
		if entry.Command != command || filepath.Clean(entry.Directory) != directory {
			continue
		}
		selected, err := rubyConfigurationGemfile(root, entry.Directory, entry.Environment)
		if err != nil || selected != gemfile {
			continue
		}
		matches := true
		for key, required := range entry.Environment {
			if key == "BUNDLE_GEMFILE" {
				continue
			}
			actual, ok := t.configurationEnvironment[key]
			if !ok {
				actual = os.Getenv(key)
			}
			matches = matches && actual == required
		}
		if matches {
			locations = append(locations, entry.Locations...)
		}
	}
	return locations, nil
}

func summarizeRubyRuntime(ci *onboard.RuntimeCheck) {
	if len(ci.Jobs) == 0 {
		return
	}
	ci.Status, ci.Reason = "compatible", "Static Ruby CI selections agree with their selected bundles. Actual CI execution remains untested."
	instrumented := false
	for _, job := range ci.Jobs {
		instrumented = instrumented || job.Status == "compatible"
		if job.Status != "compatible" && job.Status != "excluded" {
			ci.Status, ci.Reason = "inconclusive", job.Reason
			if job.Status == "incompatible" {
				ci.Status = "incompatible"
			}
			return
		}
	}
	if !instrumented {
		ci.Status, ci.Reason = "inconclusive", "Every CI entry excludes instrumentation; at least one compatible instrumented CI consumer is required."
	}
}

func mergeRubyRuntimeChecks(current, next *onboard.RuntimeCheck) *onboard.RuntimeCheck {
	if current == nil {
		return next
	}
	for _, finding := range next.Jobs {
		index := slices.IndexFunc(current.Jobs, func(job onboard.RuntimeFinding) bool {
			return finding.Location != "" && job.Location == finding.Location
		})
		if index >= 0 {
			current.Jobs[index] = finding
		} else {
			current.Jobs = append(current.Jobs, finding)
		}
	}
	summarizeRubyRuntime(current)
	return current
}
