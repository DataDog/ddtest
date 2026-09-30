package platform

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
)

//go:embed scripts/ruby_env.rb
var rubyEnvScript string

const (
	rubyOptEnvVar       = "RUBYOPT"
	rubyOptDefaultValue = "-rbundler/setup -rdatadog/ci/auto_instrument"
)

type Ruby struct {
	frameworkEnv      map[string]string
	executor          commandExecutor
	testSkippingLevel settings.TestSkippingLevel
}

func NewRuby(testSkippingLevel settings.TestSkippingLevel) *Ruby {
	return &Ruby{
		executor:          &ext.DefaultCommandExecutor{},
		testSkippingLevel: testSkippingLevel,
	}
}

func (r *Ruby) Name() string {
	return "ruby"
}

func (r *Ruby) Detect(repositoryRoot string) (bool, error) {
	return detectAnyFile(repositoryRoot, "Gemfile")
}

func (r *Ruby) DetectFramework() (framework.Framework, error) {
	root := "."
	hint := settings.GetFramework()
	candidates := []framework.Framework{framework.NewRSpec(r), framework.NewMinitest(r)}
	if hint == "" {
		candidates = nil
		gemfile, err := os.ReadFile(filepath.Join(root, "Gemfile"))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		rspec, err := detectAnyFile(root, ".rspec")
		if err != nil {
			return nil, err
		}
		if rspec || strings.Contains(string(gemfile), "rspec") {
			candidates = append(candidates, framework.NewRSpec(r))
		}
		tests, err := os.Stat(filepath.Join(root, "test"))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err == nil && tests.IsDir() || strings.Contains(string(gemfile), "minitest") {
			candidates = append(candidates, framework.NewMinitest(r))
		}
	}
	fw, err := selectFramework(r.Name(), hint, candidates)
	if err != nil {
		return nil, err
	}
	r.frameworkEnv = r.baseEnv()
	return fw, nil
}

func (r *Ruby) TestSkippingLevel() settings.TestSkippingLevel {
	return r.testSkippingLevel
}

// baseEnv returns environment variables required for Ruby commands.
// It sets RUBYOPT to auto-instrument with datadog-ci if not already set.
func (r *Ruby) baseEnv() map[string]string {
	envMap := make(map[string]string)

	// Check if RUBYOPT is already set in the environment
	if _, exists := os.LookupEnv(rubyOptEnvVar); !exists {
		slog.Debug("Setting RUBYOPT to auto-instrument with datadog-ci", "rubyOpt", rubyOptDefaultValue)

		envMap[rubyOptEnvVar] = rubyOptDefaultValue
	}

	return envMap
}

func (r *Ruby) CreateTagsMap(ctx context.Context) (map[string]string, error) {
	tags := make(map[string]string)
	tags["language"] = r.Name()

	// Create plan directory if it doesn't exist
	if err := os.MkdirAll(constants.PlanDirectory, 0755); err != nil {
		return nil, fmt.Errorf("failed to create plan directory: %w", err)
	}

	// Create a temporary file for the Ruby script output
	tempFile := constants.RubyEnvOutputPath
	defer func() { _ = os.Remove(tempFile) }()

	// Execute the embedded Ruby script to get runtime tags
	args := []string{"exec", "ruby", "-e", rubyEnvScript, tempFile}
	if output, err := r.executor.CombinedOutput(ctx, "bundle", args, nil); err != nil {
		return nil, runtimeTagProbeError("failed to execute Ruby script", output, err)
	}

	// Read the JSON output from the temp file
	fileContent, err := os.ReadFile(tempFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read Ruby script output file: %w", err)
	}

	// Parse the JSON output
	var rubyTags map[string]string
	if err := json.Unmarshal(fileContent, &rubyTags); err != nil {
		return nil, fmt.Errorf("failed to parse runtime tags JSON: %w, tried to parse: %s", err, string(fileContent))
	}

	// Merge the tags from the Ruby output
	maps.Copy(tags, rubyTags)

	return tags, nil
}

// DetectTracer reads the project tracer's bundle information.
func (r *Ruby) DetectTracer(ctx context.Context, _ TracerOptions) (string, error) {
	gemVersion, err := r.detectTracerVersion(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("  * %s (%s)", requiredGemName, gemVersion.String()), nil
}

func (r *Ruby) InstallTestdriveTracer(ctx context.Context, options TracerOptions) (TracerInstallation, error) {
	command, args, err := r.TracerInstallCommand(options)
	if err != nil {
		return TracerInstallation{}, err
	}
	if project, err := r.DetectTracer(ctx, options); err == nil && project != "" {
		return TracerInstallation{Project: true}, nil
	}
	if output, err := r.executor.CombinedOutput(ctx, command, args, map[string]string{"RUBYOPT": ""}); err != nil {
		return TracerInstallation{}, runtimeTagProbeError("bundle add datadog-ci", output, err)
	}
	return TracerInstallation{}, nil
}

func (r *Ruby) TracerInstallCommand(options TracerOptions) (string, []string, error) {
	args := []string{"add", requiredGemName}
	if ref, ok := strings.CutPrefix(options.Version, "git:"); ok {
		if ref == "" {
			return "", nil, fmt.Errorf("tracer git ref must not be empty")
		}
		args = append(args, "--git", "https://github.com/DataDog/datadog-ci-rb.git", "--ref", ref)
	} else if options.Version != "" && options.Version != "latest" {
		args = append(args, "--version", options.Version)
	}
	return "bundle", args, nil
}

func (r *Ruby) RunEnv(options framework.RuntimeOptions) (map[string]string, error) {
	if len(options.PreloadFiles) != 0 {
		return nil, fmt.Errorf("Ruby framework preloads are not supported")
	}
	env := maps.Clone(r.frameworkEnv)
	if env == nil {
		env = r.baseEnv()
	}
	maps.Copy(env, options.Env)
	return env, nil
}

func (r *Ruby) DiscoveryEnv(ctx context.Context, kind framework.DiscoveryKind, options framework.RuntimeOptions) (map[string]string, error) {
	switch kind {
	case framework.FileDiscovery:
		return maps.Clone(options.Env), nil
	case framework.FullDiscovery:
		if err := r.SanityCheck(ctx); err != nil {
			return nil, fmt.Errorf("full test discovery requires datadog-ci: %w", err)
		}
		return r.RunEnv(options)
	default:
		return nil, fmt.Errorf("unknown discovery kind: %d", kind)
	}
}
