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
	"regexp"
	"strings"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/version"
)

//go:embed scripts/ruby_env.rb
var rubyEnvScript string

const (
	requiredGemName       = "datadog-ci"
	requiredGemMinVersion = "1.31.0"
	rubyOptEnvVar         = "RUBYOPT"
	rubyOptDefaultValue   = "-rbundler/setup -rdatadog/ci/auto_instrument"
)

type Ruby struct {
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
	candidates := []framework.Framework{framework.NewRSpec(), framework.NewMinitest()}
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
			candidates = append(candidates, framework.NewRSpec())
		}
		tests, err := os.Stat(filepath.Join(root, "test"))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err == nil && tests.IsDir() || strings.Contains(string(gemfile), "minitest") {
			candidates = append(candidates, framework.NewMinitest())
		}
	}
	fw, err := selectFramework(r.Name(), hint, candidates)
	if err != nil {
		return nil, err
	}
	fw.SetPlatformEnv(r.GetPlatformEnv())
	return fw, nil
}

func (r *Ruby) TestSkippingLevel() settings.TestSkippingLevel {
	return r.testSkippingLevel
}

// GetPlatformEnv returns environment variables required for Ruby commands.
// It sets RUBYOPT to auto-instrument with datadog-ci if not already set.
func (r *Ruby) GetPlatformEnv() map[string]string {
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

func (r *Ruby) SanityCheck(ctx context.Context) error {
	output, err := r.DetectTracer(ctx, TracerOptions{})
	if err != nil {
		return err
	}
	if output == "" {
		return fmt.Errorf("datadog-ci is not installed")
	}

	requiredVersion, err := version.Parse(requiredGemMinVersion)
	if err != nil {
		return err
	}

	gemVersion, err := parseBundlerInfoVersion(output, requiredGemName)
	if err != nil {
		return err
	}

	if gemVersion.Compare(requiredVersion) < 0 {
		return fmt.Errorf("datadog-ci gem version %s is lower than required >= %s", gemVersion.String(), requiredVersion.String())
	}

	return nil
}

// bundlerInfoRegex matches bundler info output format: "  * gem-name (version [hash])"
// Captures: 1=gem-name, 2=version
var bundlerInfoRegex = regexp.MustCompile(`^\s*\*\s+(\S+)\s+\((\d+\.\d+\.\d+)`)

func parseBundlerInfoVersion(output, gemName string) (version.Version, error) {
	for line := range strings.SplitSeq(output, "\n") {
		matches := bundlerInfoRegex.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		matchedGem := matches[1]
		if matchedGem != gemName {
			continue
		}

		versionString := matches[2]
		parsed, err := version.Parse(versionString)
		if err != nil {
			return version.Version{}, fmt.Errorf("failed to parse version from bundle info output: %w", err)
		}

		return parsed, nil
	}

	return version.Version{}, fmt.Errorf("unable to find datadog-ci gem version in bundle info output")
}

// DetectTracer reads the project tracer's bundle information.
func (r *Ruby) DetectTracer(ctx context.Context, _ TracerOptions) (string, error) {
	return tracerProbe(ctx, r.executor, "bundle", []string{"info", requiredGemName}, nil)
}

func (r *Ruby) InstallTracer(ctx context.Context, options TracerOptions) (TracerInstallation, error) {
	directory := options.Directory
	root, err := os.Getwd()
	if err != nil {
		return TracerInstallation{}, fmt.Errorf("find project root: %w", err)
	}
	selection := ""
	version := strings.NewReplacer(`\`, `\\`, "'", `\'`).Replace(options.Version)
	if ref, ok := strings.CutPrefix(version, "git:"); ok {
		if ref == "" {
			return TracerInstallation{}, fmt.Errorf("tracer git ref must not be empty")
		}
		selection = ", git: 'https://github.com/DataDog/datadog-ci-rb.git', ref: '" + ref + "'"
	} else if version != "" && version != "latest" {
		selection = ", '" + version + "'"
	}
	project, err := r.DetectTracer(ctx, options)
	if err == nil && project != "" {
		return TracerInstallation{Project: true}, nil
	}
	if strings.Contains(directory, " ") {
		return TracerInstallation{}, fmt.Errorf("the Ruby tracer's native extensions cannot build in paths containing spaces; run testdrive from a checkout without spaces")
	}
	gemfile := filepath.Join(directory, "Gemfile")
	path := strings.ReplaceAll(strings.ReplaceAll(filepath.Join(root, "Gemfile"), `\`, `\\`), "'", `\'`)
	contents := "source 'https://rubygems.org'\neval_gemfile '" + path + "'\n" +
		"gem 'datadog-ci'" + selection + "\n"
	if err := os.WriteFile(gemfile, []byte(contents), 0600); err != nil {
		return TracerInstallation{}, fmt.Errorf("write isolated Gemfile: %w", err)
	}
	if err := copyRubyLockfile(root, directory); err != nil {
		return TracerInstallation{}, err
	}
	if err := copyRubyBundleConfig(root, directory); err != nil {
		return TracerInstallation{}, err
	}
	env := rubyTracerEnvironment(gemfile)
	if output, err := r.executor.CombinedOutput(ctx, "bundle", []string{"install"}, env); err != nil {
		return TracerInstallation{}, runtimeTagProbeError("install isolated Ruby bundle", output, err)
	}
	return TracerInstallation{Path: gemfile, Env: env}, nil
}

func copyRubyBundleConfig(root, directory string) error {
	contents, err := os.ReadFile(filepath.Join(root, ".bundle", "config"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read project Bundler config: %w", err)
	}
	configDirectory := filepath.Join(directory, "bundle-config")
	if err := os.MkdirAll(configDirectory, 0700); err != nil {
		return fmt.Errorf("create isolated Bundler config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "config"), contents, 0600); err != nil {
		return fmt.Errorf("copy project Bundler config: %w", err)
	}
	return nil
}

// Preserve the customer's resolved versions while adding the tracer. PATH
// sources in a lockfile are relative to its Gemfile, so relocate those sources
// when copying it into the session. The original remains untouched.
func copyRubyLockfile(root, directory string) error {
	contents, err := os.ReadFile(filepath.Join(root, "Gemfile.lock"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read project lockfile: %w", err)
	}
	lines := strings.Split(string(contents), "\n")
	inPath := false
	for i, line := range lines {
		if line != "" && !strings.HasPrefix(line, " ") {
			inPath = line == "PATH"
		}
		if inPath && strings.HasPrefix(line, "  remote: ") {
			path := strings.TrimPrefix(line, "  remote: ")
			if !filepath.IsAbs(path) {
				lines[i] = "  remote: " + filepath.Join(root, path)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(directory, "Gemfile.lock"), []byte(strings.Join(lines, "\n")), 0600); err != nil {
		return fmt.Errorf("copy project lockfile: %w", err)
	}
	return nil
}

func rubyTracerEnvironment(gemfile string) map[string]string {
	if gemfile == "" {
		return nil
	}
	return map[string]string{
		"BUNDLE_GEMFILE":    gemfile,
		"BUNDLE_PATH":       filepath.Join(filepath.Dir(gemfile), "gems"),
		"BUNDLE_APP_CONFIG": filepath.Join(filepath.Dir(gemfile), "bundle-config"),
		"BUNDLE_FROZEN":     "false", "BUNDLE_DEPLOYMENT": "false",
		"BUNDLE_WITH": "", "BUNDLE_WITHOUT": "", "BUNDLE_ONLY": "",
	}
}
