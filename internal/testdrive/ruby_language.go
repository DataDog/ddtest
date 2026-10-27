package testdrive

import (
	"context"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

// rubyLanguage observes RSpec and Minitest suites with datadog-ci.
type rubyLanguage struct{}

func (rubyLanguage) name() string         { return "ruby" }
func (rubyLanguage) frameworks() []string { return []string{"rspec", "minitest"} }
func (rubyLanguage) displayNames() map[string]string {
	return map[string]string{"ruby": "Ruby", "rspec": "RSpec", "minitest": "Minitest"}
}
func (rubyLanguage) tracerPackage() string { return "datadog-ci" }

// Bundler lists the gem as "* datadog-ci (<version>)".
func (rubyLanguage) installedTracerVersion(detected string) string {
	if info, ok := strings.CutPrefix(strings.TrimSpace(detected), "* datadog-ci ("); ok {
		version, _, _ := strings.Cut(info, ")")
		return version
	}
	return ""
}

func (rubyLanguage) prepareStorage() error { return nil }

func (rubyLanguage) inspectEnvironment(ctx context.Context, request environmentRequest) (string, []string, environmentInfo, *reportCard) {
	return request.command, request.args, rubyEnvironmentInfo(ctx), nil
}

func (rubyLanguage) parsesCommandAssignments() bool { return false }

func (rubyLanguage) tracerCommand(_ environmentInfo, command string, args []string) (string, []string) {
	return command, args
}

func (rubyLanguage) installPreview(string) string { return "" }

// A temporary installation goes through Bundler, which edits the Gemfile.
func (rubyLanguage) dependencyChanges(projectTracer string) ([]string, string) {
	if projectTracer == "" {
		return nil, "Bundler updates Gemfile and Gemfile.lock."
	}
	return []string{"Gemfile", "Gemfile.lock", "gems.rb", "gems.locked"}, ""
}

func (rubyLanguage) installDirectory(sessionDirectory, _ string) (string, func() error, error) {
	return sessionDirectory, nil, nil
}

func (rubyLanguage) projectTracerPath(string) string { return "" }
func (rubyLanguage) temporaryTracerSource() string   { return "project bundle installation" }

func (rubyLanguage) runEnvironment(_ *Testdrive, path string) map[string]string {
	return rubyEnvironment(path)
}

func (rubyLanguage) noResultsFinding(reportRuntime, bool, bool, *reportCard, *intake.Advice) {}

func (rubyLanguage) pairedFrameworks() map[string]pairedFramework { return nil }
func (rubyLanguage) isolatesConfigurations() bool                 { return false }

func (rubyLanguage) prepareConfiguration(*Testdrive, *configurationResult, string, string) error {
	return nil
}
func (rubyLanguage) coordinated() bool        { return false }
func (rubyLanguage) defaultsToCITracer() bool { return false }

// RubyGems requirements are checked against the installed bundle, so CI
// inspection only inventories the static Ruby and tracer selections.
func (rubyLanguage) ciRuntimes(_ context.Context, root, framework string) onboard.RuntimeCheck {
	return onboard.RubyCIConfiguration(root, framework)
}
