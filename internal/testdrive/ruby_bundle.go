package testdrive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/kballard/go-shellquote"
)

// Loading project Ruby can run its code, so checks use the same owned-copy
// boundary as tests. Within one coordinated validation (t.reuseCheckCopy),
// the readiness and packaging checks share one copy; releaseCheckCopy removes
// it when the invocation ends.
func (t *Testdrive) inRubyWorkflowCopy(ctx context.Context, check func(string) error) (runErr error) {
	if t.checkCopy != "" {
		return check(filepath.Join(t.checkCopy, "checkout"))
	}
	parent, err := os.MkdirTemp("", "ddtest-ruby-check-")
	if err != nil {
		return err
	}
	if err := workspace.MarkStorage(parent, t.ownershipRoot()); err != nil {
		_ = os.Remove(parent)
		return err
	}
	keep := false
	defer func() {
		if !keep {
			runErr = errors.Join(runErr, workspace.RemoveMarkedStorage(parent))
		}
	}()
	size, err := configurationCopyEstimate(ctx, t.repositoryRoot, nil)
	if err != nil {
		return err
	}
	if err := checkConfigurationSpace(parent, size); err != nil {
		return err
	}
	root := filepath.Join(parent, "checkout")
	if err := workspace.Copy(ctx, t.repositoryRoot, root, nil); err != nil {
		return err
	}
	if t.reuseCheckCopy {
		t.checkCopy, keep = parent, true
	}
	return check(root)
}

// Return a checkout-relative manifest while preserving Bundler's cwd semantics.
// Only a local command inherits the shell's BUNDLE_GEMFILE; CI never sees it.
func rubyConfigurationGemfile(root, directory string, env map[string]string, inheritShell bool) (string, error) {
	root = workspace.CanonicalRoot(root)
	gemfile, explicit := env["BUNDLE_GEMFILE"]
	if !explicit && inheritShell {
		gemfile = os.Getenv("BUNDLE_GEMFILE")
	}
	if gemfile == "" {
		for current := workspace.CanonicalRoot(filepath.Join(root, directory)); ; current = filepath.Dir(current) {
			relative, err := filepath.Rel(root, current)
			if err != nil || !filepath.IsLocal(relative) {
				return "", fmt.Errorf("BUNDLE_GEMFILE is outside the repository; use a repository-local Gemfile for isolated Ruby validation")
			}
			// Bundler checks both names at each level before searching parents.
			for _, name := range []string{"gems.rb", "Gemfile"} {
				candidate := filepath.Join(current, name)
				if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
					gemfile = candidate
					break
				}
			}
			if gemfile != "" || current == root {
				break
			}
		}
		if gemfile == "" {
			gemfile = "Gemfile"
		}
	}
	if !filepath.IsAbs(gemfile) {
		gemfile = filepath.Join(root, directory, gemfile)
	}
	relative, err := filepath.Rel(workspace.CanonicalRoot(root), workspace.CanonicalRoot(gemfile))
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

// Bind one bundle's metadata only to its discovered CI consumers. Focused
// commands can claim matching locations, never unrelated bundles or commands.
func (t *Testdrive) rubyRuntimeLocations() ([]string, error) {
	if len(t.configurationLocations) > 0 {
		return t.configurationLocations, nil
	}
	root := t.checkoutRoot()
	directory, err := filepath.Rel(root, t.repositoryRoot)
	if err != nil {
		return nil, err
	}
	gemfile, err := rubyConfigurationGemfile(root, directory, t.configurationEnvironment, true)
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
		selected, err := rubyConfigurationGemfile(root, entry.Directory, entry.Environment, false)
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
