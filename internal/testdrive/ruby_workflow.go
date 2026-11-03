// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
)

// currentRubyVersion identifies the Ruby on PATH that runs the selected
// commands; a saved report is bound to it. Empty when Ruby cannot run.
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
	if t.runtimeVersion != nil {
		return t.runtimeVersion()
	}
	return currentRubyVersion(t.repositoryRoot)
}

// rubyBundleReadiness inventories every selected bundle before any native
// suite or probe runs. It neither installs gems nor changes lockfiles: the
// check runs in an owned copy and returns the commands that set up each
// bundle Bundler reports as missing.
func (t *Testdrive) rubyBundleReadiness(ctx context.Context, scope onboard.ValidationScope) ([][]string, error) {
	var missing [][]string
	err := t.inRubyWorkflowCopy(ctx, func(root string) error {
		for _, entry := range scope.Commands {
			if ciInstrumentationDisabled(entry) {
				continue
			}
			// The check sees the same environment as the consumer's execution,
			// including the variables every GitHub Actions step has.
			env := isolatedCIEnvironment(rubyBundleEnvironment(entry), t.repositoryRoot, root)
			// Only a focused local command inherits the shell's BUNDLE_GEMFILE.
			local := len(entry.Locations) == 0
			gemfile, err := rubyConfigurationGemfile(t.repositoryRoot, entry.Directory, env, local)
			if err != nil {
				return err
			}
			env["BUNDLE_GEMFILE"] = filepath.Join(root, gemfile)
			env["RUBYOPT"] = ""
			lockfile, err := rubySelectedLockfile(t.repositoryRoot, root, entry.Directory, gemfile, env, local)
			if err != nil {
				return err
			}
			if _, configured := env["BUNDLE_LOCKFILE"]; configured {
				env["BUNDLE_LOCKFILE"] = lockfile
			}
			_, lockErr := os.Stat(lockfile)
			var checkErr error
			if lockErr == nil {
				if _, ok := t.executor.(directoryExecutor); !ok {
					return errors.New("ruby readiness requires a working-directory command executor")
				}
				// The check runs without the configuration's omitted secrets, as
				// its validation does: a Gemfile may read one.
				executor := configurationExecutor{commandExecutor: t.executor, without: entry.WithoutCIValues}
				_, checkErr = executor.CombinedOutputInDirectory(ctx, filepath.Join(root, entry.Directory), "bundle", []string{"check", "--dry-run"}, env)
			}
			if lockErr != nil || checkErr != nil {
				command, err := rubyBundleSetupCommand(entry, gemfile)
				if err != nil {
					return err
				}
				if !slices.ContainsFunc(missing, func(existing []string) bool { return slices.Equal(existing, command) }) {
					missing = append(missing, command)
				}
			}
		}
		return nil
	})
	return missing, err
}

// rubyBundleEnvironment is a discovered consumer's own bundle environment. A
// CI consumer selects only its declared manifest, never the shell's, and sees
// the variables every GitHub Actions step has, as its execution does.
func rubyBundleEnvironment(entry onboard.TestCommand) map[string]string {
	env := map[string]string{}
	if len(entry.Locations) > 0 {
		env = onboard.WithCIProviderDefaults(entry.Environment)
		if _, configured := env["BUNDLE_GEMFILE"]; !configured {
			env["BUNDLE_GEMFILE"] = ""
		}
		return env
	}
	maps.Copy(env, entry.Environment)
	return env
}

// rubySelectedLockfile is the lockfile Bundler uses for gemfile inside the
// copy at root: BUNDLE_LOCKFILE when set (relative paths start at the
// consumer's working directory, Bundler's cwd), otherwise the manifest's own.
// Only a focused local command inherits the shell's BUNDLE_LOCKFILE.
func rubySelectedLockfile(original, root, directory, gemfile string, env map[string]string, inheritShell bool) (string, error) {
	lock, configured := env["BUNDLE_LOCKFILE"]
	if !configured && inheritShell {
		lock = os.Getenv("BUNDLE_LOCKFILE")
	}
	if lock == "" {
		return filepath.Join(root, rubyLockfile(gemfile)), nil
	}
	if !filepath.IsAbs(lock) {
		lock = filepath.Join(root, directory, lock)
	} else if relative, err := filepath.Rel(workspace.CanonicalRoot(original), workspace.CanonicalRoot(lock)); err == nil && filepath.IsLocal(relative) {
		lock = filepath.Join(root, relative)
	}
	relative, err := filepath.Rel(root, lock)
	if err != nil || !filepath.IsLocal(relative) {
		return "", fmt.Errorf("BUNDLE_LOCKFILE is outside the repository; use a repository-local lockfile for isolated Ruby validation")
	}
	return lock, nil
}

// rubyBundleSetupCommand installs one selected bundle from the repository
// root with the consumer's own working directory and environment, so bundle
// locations such as BUNDLE_PATH and BUNDLE_APP_CONFIG match its CI step.
func rubyBundleSetupCommand(entry onboard.TestCommand, gemfile string) ([]string, error) {
	directory := filepath.Clean(entry.Directory)
	onlyGemfile := len(entry.Locations) == 0
	for key := range entry.Environment {
		onlyGemfile = onlyGemfile && key == "BUNDLE_GEMFILE"
	}
	if onlyGemfile && directory == "." && os.Getenv("RUBYOPT") == "" && os.Getenv("BUNDLE_LOCKFILE") == "" {
		return []string{"bundle", "install", "--gemfile", filepath.ToSlash(gemfile)}, nil
	}
	selected, err := filepath.Rel(directory, gemfile)
	if err != nil {
		return nil, err
	}
	env := rubyBundleEnvironment(entry)
	// Application preloads must not run from native extension build directories.
	env["RUBYOPT"] = ""
	command := []string{"env"}
	for _, key := range slices.Sorted(maps.Keys(env)) {
		command = append(command, key+"="+env[key])
	}
	// Ruby's own -C keeps cwd semantics portable without constructing shell code.
	return append(command, "ruby", "-C", directory, "-S", "bundle", "install", "--gemfile", filepath.ToSlash(selected)), nil
}

// The inventory goes to a private file: gemspecs and their Git commands may
// print diagnostics on stdout or stderr.
const rubyPackageFilesScript = `require 'json'; require 'rubygems'
spec = Gem::Specification.load(ARGV.fetch(0))
abort 'gemspec did not load' unless spec
File.write(ARGV.fetch(1), JSON.generate(spec.files))
`

// ownedGemspecRepository gives the copy its own Git repository with the
// customer's history, tags and HEAD, so gemspecs using git ls-files, git
// describe, git log or git ls-tree HEAD load as they do in a release. The
// clone reads the customer's objects through alternates and has no remote;
// nothing is written to the customer repository. Without a readable
// repository, an empty one keeps the index-only behavior.
func (t *Testdrive) ownedGemspecRepository(ctx context.Context, executor directoryExecutor, root string, gitEnv map[string]string) error {
	clone := filepath.Join(root, ".ddtest-gemspec-git")
	if _, err := executor.CombinedOutputInDirectory(ctx, root, "git", []string{"clone", "--quiet", "--shared", "--no-checkout", "--", t.repositoryRoot, clone}, nil); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_ = os.RemoveAll(clone)
		_, err := executor.CombinedOutputInDirectory(ctx, root, "git", []string{"init", "--quiet"}, gitEnv)
		return err
	}
	if err := os.Rename(filepath.Join(clone, ".git"), filepath.Join(root, ".git")); err != nil {
		return err
	}
	if err := os.RemoveAll(clone); err != nil {
		return err
	}
	// A push or fetch from gemspec code must not reach the customer repository.
	if _, err := executor.CombinedOutputInDirectory(ctx, root, "git", []string{"remote", "remove", "origin"}, gitEnv); err != nil {
		return err
	}
	// Match the customer's checked-out commit, including a detached HEAD.
	if head, err := executor.CombinedOutputInDirectory(ctx, t.repositoryRoot, "git", []string{"rev-parse", "--verify", "--quiet", "HEAD"}, nil); err == nil {
		if commit := strings.TrimSpace(string(head)); commit != "" {
			if _, err := executor.CombinedOutputInDirectory(ctx, root, "git", []string{"update-ref", "--no-deref", "HEAD", commit}, gitEnv); err != nil {
				return err
			}
		}
	}
	return nil
}

// rubyPackageExclusions loads every repository gemspec in an owned copy and
// fails when a gem would package the retained reports. Nothing is built or
// published.
func (t *Testdrive) rubyPackageExclusions(ctx context.Context) onboard.WorkflowCheck {
	check := onboard.WorkflowCheck{Name: "package_report_exclusions", Status: "passed"}
	var specs []string
	bundles := t.rubyBundlePaths()
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
			// Dependency/configuration trees, installed gems and test fixtures
			// aren't publish targets: an installed gem's specifications/*.gemspec
			// lists no repository files.
			if relative != "." && (slices.Contains([]string{".git", ".bundle", "vendor", "node_modules", ".testoptimization", "test", "tests", "spec", "fixtures"}, entry.Name()) ||
				installedGemDirectory(entry.Name()) || rubyGemsInstallation(path) || slices.Contains(bundles, path)) {
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
			return t.checkRubyPackages(ctx, root, specs)
		})
	}
	if err != nil {
		check.Status, check.Reason = "failed", reportText(err.Error())
	} else {
		check.Reason = "Repository gemspec inventories exclude both reports; loaded only in an owned copy, without building or publishing a gem."
	}
	return check
}

// rubyBundlePaths are the configured BUNDLE_PATH directories inside the
// repository: the shell's, the selected configuration's and every discovered
// CI consumer's.
func (t *Testdrive) rubyBundlePaths() []string {
	type located struct{ directory, path string }
	candidates := []located{{".", os.Getenv("BUNDLE_PATH")}, {".", t.configurationEnvironment["BUNDLE_PATH"]}}
	if t.workflowScope != nil {
		for _, entry := range t.workflowScope.Commands {
			candidates = append(candidates, located{entry.Directory, entry.Environment["BUNDLE_PATH"]})
		}
	}
	var paths []string
	for _, candidate := range candidates {
		if candidate.path == "" {
			continue
		}
		path := candidate.path
		if !filepath.IsAbs(path) {
			path = filepath.Join(t.repositoryRoot, candidate.directory, path)
		}
		paths = append(paths, filepath.Clean(path))
	}
	return paths
}

// rubyGemsInstallation reports whether directory is a RubyGems installation
// root, such as <BUNDLE_PATH>/ruby/3.4.0, which holds both gems and
// specifications.
func rubyGemsInstallation(directory string) bool {
	for _, name := range []string{"gems", "specifications"} {
		if info, err := os.Stat(filepath.Join(directory, name)); err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

func (t *Testdrive) checkRubyPackages(ctx context.Context, root string, specs []string) error {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	executor, ok := t.executor.(directoryExecutor)
	if !ok {
		return errors.New("gemspec inspection requires a working-directory executor")
	}
	// Replace copied metadata (including worktree pointers) with an owned
	// repository, so a gemspec that runs Git never writes to the customer's.
	if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
		return err
	}
	gitEnv := map[string]string{"GIT_DIR": filepath.Join(root, ".git"), "GIT_WORK_TREE": root}
	if err := t.ownedGemspecRepository(ctx, executor, root, gitEnv); err != nil {
		return err
	}
	data, err := executor.CombinedOutputInDirectory(ctx, t.repositoryRoot, "git", []string{"rev-parse", "--git-path", "index"}, nil)
	if err != nil {
		return err
	}
	index := strings.TrimSpace(string(data))
	if !filepath.IsAbs(index) {
		index = filepath.Join(t.repositoryRoot, index)
	}
	if contents, err := os.ReadFile(index); err == nil {
		if err := os.WriteFile(filepath.Join(root, ".git", "index"), contents, 0600); err != nil {
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
			env := maps.Clone(gitEnv)
			env["RUBYOPT"] = ""
			files, err := t.gemspecFiles(ctx, executor, directory, filepath.Join(root, spec), env)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				loadErr = fmt.Errorf("inspect %s packaging: %w", spec, err)
				continue
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
}

// gemspecFiles loads one gemspec's file inventory through a private result
// file; the command's own output is only a diagnostic.
func (t *Testdrive) gemspecFiles(ctx context.Context, executor directoryExecutor, directory, spec string, env map[string]string) ([]string, error) {
	result, err := os.CreateTemp("", "ddtest-gemspec-files-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(result.Name()) }()
	if err := result.Close(); err != nil {
		return nil, err
	}
	output, err := executor.CombinedOutputInDirectory(ctx, directory, "ruby", []string{"-e", rubyPackageFilesScript, spec, result.Name()}, env)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, commandDiagnostic(output))
	}
	data, err := os.ReadFile(result.Name())
	if err != nil {
		return nil, err
	}
	var files []string
	if err := json.Unmarshal(data, &files); err != nil {
		return nil, fmt.Errorf("%s did not produce a file inventory: %w", filepath.Base(spec), err)
	}
	return files, nil
}

// fingerprintRubyBundles binds receipts to the actual bundles and settings,
// including ignored lockfiles and Bundler configuration. Read only: no
// Gemfile or other repository Ruby is evaluated. Configs may contain
// credentials; only their digest becomes part of the receipt.
func fingerprintRubyBundles(ctx context.Context, root string) (string, error) {
	root = workspace.CanonicalRoot(root)
	commands := []onboard.TestCommand{{Directory: "."}}
	for _, name := range (rubyLanguage{}).frameworks() {
		// Unresolved discovery already blocks completion. Its source is still
		// fingerprinted; only statically selected consumers add bundle inputs.
		if scope, err := onboard.DiscoverValidationScope(root, name); err == nil {
			commands = append(commands, scope.Commands...)
		}
	}
	paths := map[string]bool{}
	// Ignored lockfiles of every repository manifest, such as gems.locked or
	// gemfiles/*.gemfile.lock, select the installed bundle too.
	manifests, err := rubyRepositoryManifests(ctx, root)
	if err != nil {
		return "", err
	}
	for _, manifest := range manifests {
		paths[filepath.Join(root, rubyLockfile(manifest))] = true
	}
	for i, command := range commands {
		local := len(command.Locations) == 0
		gemfile, err := rubyConfigurationGemfile(root, command.Directory, command.Environment, local)
		if err != nil {
			// An unrelated ambient bundle must not block the repository's own.
			if i == 0 {
				continue
			}
			return "", err
		}
		manifest := filepath.Join(root, gemfile)
		if i == 0 {
			if _, err := os.Stat(manifest); os.IsNotExist(err) {
				continue
			}
		}
		value := func(key string) string {
			if value, ok := command.Environment[key]; ok {
				return value
			}
			return os.Getenv(key)
		}
		lock := value("BUNDLE_LOCKFILE")
		if lock == "" {
			lock = filepath.Join(root, rubyLockfile(gemfile))
		} else if !filepath.IsAbs(lock) {
			lock = filepath.Join(root, command.Directory, lock)
		}
		paths[manifest], paths[lock] = true, true
		bundleRoot := filepath.Dir(manifest)
		appConfig := value("BUNDLE_APP_CONFIG")
		_, configured := command.Environment["BUNDLE_APP_CONFIG"]
		if _, ambient := os.LookupEnv("BUNDLE_APP_CONFIG"); !configured && !ambient {
			appConfig = ".bundle"
		}
		if !filepath.IsAbs(appConfig) {
			appConfig = filepath.Join(bundleRoot, appConfig)
		}
		paths[filepath.Join(appConfig, "config")] = true
		global := value("BUNDLE_CONFIG")
		if global == "" {
			global = value("BUNDLE_USER_CONFIG")
		}
		if global == "" {
			home := value("BUNDLE_USER_HOME")
			if home == "" {
				home = filepath.Join(value("HOME"), ".bundle")
			}
			global = filepath.Join(home, "config")
		}
		if !filepath.IsAbs(global) {
			global = filepath.Join(root, command.Directory, global)
		}
		paths[global] = true
	}
	if len(paths) == 0 {
		return "", nil
	}
	var hash strings.Builder
	for _, item := range slices.Sorted(slices.Values(os.Environ())) {
		if strings.HasPrefix(item, "BUNDLE_") {
			_, _ = fmt.Fprintf(&hash, "bundler-env\x00%s\x00", item)
		}
	}
	for _, key := range []string{"RUBYOPT", "RUBYLIB", "GEM_HOME", "GEM_PATH"} {
		_, _ = fmt.Fprintf(&hash, "%s=%s\x00", key, os.Getenv(key))
	}
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(&hash, "ruby-input\x00%s\x00", path)
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			hash.WriteString("missing\x00")
			continue
		}
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("ruby bundle input is not a regular file: %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		hash.Write(data)
		hash.WriteString("\x00")
	}
	return hash.String(), nil
}

// rubyRepositoryManifests lists the checkout's Gemfile-like manifests that Git
// tracks or would track; outside Git, every one in the tree.
func rubyRepositoryManifests(ctx context.Context, root string) ([]string, error) {
	isManifest := func(name string) bool {
		base := filepath.Base(name)
		return (base == "gems.rb" || strings.HasPrefix(base, "Gemfile") || strings.HasSuffix(base, ".gemfile")) && !strings.HasSuffix(base, ".lock")
	}
	var manifests []string
	data, err := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err == nil {
		for _, name := range strings.Split(string(data), "\x00") {
			if name != "" && isManifest(name) {
				manifests = append(manifests, filepath.FromSlash(name))
			}
		}
		return manifests, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && slices.Contains([]string{".git", "node_modules", ".testoptimization", "vendor", ".bundle"}, entry.Name()) {
			return filepath.SkipDir
		}
		if !entry.IsDir() && isManifest(entry.Name()) {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			manifests = append(manifests, relative)
		}
		return nil
	})
	return manifests, err
}
