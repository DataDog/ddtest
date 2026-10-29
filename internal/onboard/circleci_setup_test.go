// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// circleRubyRepository is an RSpec repository whose only CI is the
// CircleCI configuration config; the test runs in it with CircleCI selected.
func circleRubyRepository(t *testing.T, config string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "Gemfile", "source 'https://rubygems.org'\ngem 'rspec'\ngem 'rake'\n")
	writeFile(t, root, "Rakefile", "require 'rspec/core/rake_task'\n\nRSpec::Core::RakeTask.new(:spec)\n\ntask default: :spec\n")
	writeFile(t, root, circleCIFile, config)
	t.Chdir(root)
	useCIProvider(t, "circleci")
	return root
}

const circleRubyOrb = "      - test-optimization-circleci-orb/autoinstrument:\n          languages: ruby\n          site: datadoghq.com\n          ruby_tracer_version: 1.42.0\n"

// circleRubyJob is an RSpec job in image with steps before and after the
// orb step.
func circleRubyJob(image, environment, before, after string) string {
	return "version: 2.1\norbs:\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\njobs:\n  rspec:\n    docker:\n      - image: " + image + "\n" + environment + "    steps:\n      - checkout\n      - run: bundle install\n" + before + circleRubyOrb + after + "      - run: bundle exec rspec\n" + circleWorkflow("      - rspec\n")
}

// The shared completeness check reads the orb on CircleCI: the language,
// a supported release, an exact tracer release and the selected site,
// before every test command, on a Ruby the image selects.
func TestCircleCIRubySetupCompleteness(t *testing.T) {
	complete := circleRubyJob("cimg/ruby:3.3", "", "", "")
	for _, tc := range []struct {
		name, config, site string
		configured         bool
		reason             string
	}{
		{"complete", complete, "", true, ""},
		{"selected site", complete, "datadoghq.com", true, ""},
		{"other site", complete, "datadoghq.eu", false, "set site: datadoghq.eu on the step"},
		{"missing", strings.Replace(complete, circleRubyOrb, "", 1), "", false, "Run the Datadog Test Optimization orb with an exact ruby_tracer_version"},
		{"latest tracer", strings.Replace(complete, "1.42.0", "latest", 1), "", false, "is not an exact release"},
		{"unsupported orb", strings.Replace(complete, "orb@2", "orb@1", 1), "", false, "is not a supported release"},
		{"official image", circleRubyJob("ruby:3.3", "", "", ""), "", true, ""},
		{"floating image", circleRubyJob("cimg/ruby:current", "", "", ""), "", true, "in a job whose image selects a static Ruby version"},
		{"replaced preload", circleRubyJob("cimg/ruby:3.3", "", "", "      - run: echo 'export RUBYOPT=-W0' >> \"$BASH_ENV\"\n"), "", true, "replaces the RUBYOPT the Datadog Test Optimization orb exports"},
		{"extended preload", circleRubyJob("cimg/ruby:3.3", "", "", "      - run: echo 'export RUBYOPT=\"$RUBYOPT -W0\"' >> \"$BASH_ENV\"\n"), "", true, ""},
		// $BASH_ENV overrides a step's environment, so the preload stays.
		{"step environment after the orb", strings.Replace(complete, "      - run: bundle exec rspec\n", "      - run:\n          command: bundle exec rspec\n          environment:\n            RUBYOPT: -W0\n", 1), "", true, ""},
		{"job options before the orb", circleRubyJob("cimg/ruby:3.3", "    environment:\n      RUBYOPT: -W0\n", "", ""), "", true, "dropping the job's -W0; restore it after the Datadog orb step with a later step that appends it to $BASH_ENV"},
		// brakeman runs bundle exec rake RUBYOPT='--enable-frozen-string-literal ...'.
		{"rake RUBYOPT argument", strings.Replace(complete, "bundle exec rspec\n", "bundle exec rake RUBYOPT='--enable-frozen-string-literal --debug-frozen-string-literal'\n", 1), "", true, "bundle exec rake sets RUBYOPT='--enable-frozen-string-literal --debug-frozen-string-literal' as a rake argument"},
		{"rake RUBYOPT argument keeping the preload", strings.Replace(complete, "bundle exec rspec\n", "bundle exec rake RUBYOPT=\"$RUBYOPT --enable-frozen-string-literal\"\n", 1), "", true, ""},
		{"Ruby 3.0 RubyGems", circleRubyJob("cimg/ruby:3.0", "", "", ""), "", true, "Run gem update --system 3.4.22 before it"},
		{"Ruby 3.0 updated RubyGems", circleRubyJob("cimg/ruby:3.0", "", "      - run: gem update --system 3.4.22\n", ""), "", true, ""},
		{"JRuby job excluded", strings.Replace(strings.Replace(complete, "\nworkflows:", "  jruby:\n    docker:\n      - image: jruby:9.4\n    steps:\n      - run: bundle exec rspec\n\nworkflows:", 1), "      - rspec\n", "      - rspec\n      - jruby\n", 1), "", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := circleRubyRepository(t, tc.config)
			require.Equal(t, tc.configured, rubyDiscovery{}.ciSetupConfigured(root, "rspec", tc.site))
			var reasons []string
			for _, job := range RubyCIConfigurationForSite(root, "rspec", tc.site).Jobs {
				reasons = append(reasons, job.Status+": "+job.Reason)
			}
			if tc.reason != "" {
				require.Contains(t, strings.Join(reasons, "\n"), "inconclusive: ")
				require.Contains(t, strings.Join(reasons, "\n"), tc.reason)
			} else if tc.configured {
				require.NotContains(t, strings.Join(reasons, "\n"), "inconclusive: ")
			}
		})
	}
}

// Agent onboarding asks for the orb with the selected site, then hands off
// validation once every test job runs it.
func TestCircleCIRubyAgentOnboarding(t *testing.T) {
	root := circleRubyRepository(t, strings.Replace(circleRubyJob("cimg/ruby:3.3", "", "", ""), circleRubyOrb, "", 1))
	state, err := rubyAgentStart(t, StartOptions{Format: "json", Site: "datadoghq.eu"})
	require.Error(t, err)
	require.Equal(t, "ci_setup_required", state.Code)
	require.Contains(t, state.Reason, "CI does not yet run the Datadog Test Optimization orb")
	require.Contains(t, state.Instruction, "# Enable Datadog Test Optimization for rspec on CircleCI")
	require.Contains(t, state.Instruction, "- test-optimization-circleci-orb/autoinstrument:\n    languages: ruby\n    site: datadoghq.eu\n    ruby_tracer_version: <exact datadog-ci release>\n")
	require.Contains(t, state.Instruction, "Without it the Datadog orb step fails, which fails the job")
	require.Contains(t, state.Instruction, "do not edit the Gemfile")
	require.NotContains(t, state.Instruction, "test-visibility-github-action")
	require.NotContains(t, state.Instruction, "GitLab")

	writeFile(t, root, circleCIFile, strings.Replace(circleRubyJob("cimg/ruby:3.3", "", "", ""), "site: datadoghq.com", "site: datadoghq.eu", 1))
	state, err = rubyAgentStart(t, StartOptions{Format: "json", Site: "datadoghq.eu"})
	require.NoError(t, err)
	require.Equal(t, "ready", state.Status)
	require.Contains(t, state.Reason, "CI already runs the Datadog Test Optimization orb")
}

// Options that must follow the orb persist through $BASH_ENV.
func TestCircleCIMinitestInstructionsExtendTheExportedOptions(t *testing.T) {
	text := providerSetupInstructions(t.TempDir(), circleCI{}, "ruby", "minitest", "", true)
	require.Contains(t, text, "Right after the Datadog orb step, append the initializer to the exported options:")
	require.Contains(t, text, `- run: echo 'export RUBYOPT="$RUBYOPT -r./ci/datadog_test_optimization.rb"' >> "$BASH_ENV"`)
	require.NotContains(t, text, "${{ env.RUBYOPT }}")
	text = providerSetupInstructions(t.TempDir(), gitLabCI{}, "ruby", "minitest", "", true)
	require.Contains(t, text, "Right after the Datadog setup entry, append the initializer to the exported options:")
}

// The committed fixture is rubyXL's configuration: a Ruby version matrix
// on cimg/ruby images with test splitting. Its Ruby 3.0 entry needs a
// RubyGems update, which a when step on the image guards per entry.
func TestCircleCIRubyFixture(t *testing.T) {
	root := copyFixture(t, "circleci-ruby")
	useCIProvider(t, "circleci")
	scope, err := DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "bundle exec rspec --format progress", scope.Commands[0].Command)
	require.Len(t, scope.Commands[0].locations, 6)
	require.Contains(t, strings.Join(scope.Review, "\n"), "CircleCI test splitting passes each parallel container its share of the test files in $TEST_FILES")
	request := rubyDiscovery{}.ciSetupRequest(root, "rspec", "datadoghq.com", circleCI{}.setupTerms())
	notes := strings.Join(request.Notes, "\n")
	guard := "- when:\n    condition:\n      equal: [ \"cimg/ruby:3.0\", \"cimg/ruby:<< parameters.ruby-version >>\" ]\n    steps:\n      - run: gem update --system 3.4.22"
	require.Contains(t, notes, "These CI jobs run cimg/ruby:3.0")
	require.Contains(t, notes, guard)

	// The agent's edit: the guard and the orb before the tests.
	data := readFixtureFile(t, root, circleCIFile)
	indented := "      " + strings.ReplaceAll(guard, "\n", "\n      ") + "\n"
	edited := strings.Replace(data, "      - run:\n          name: run tests\n", indented+circleRubyOrb+"      - run:\n          name: run tests\n", 1)
	edited = strings.Replace(edited, "version: 2.1\n", "version: 2.1\norbs:\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\n", 1)
	writeFile(t, root, circleCIFile, edited)
	require.True(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", "datadoghq.com"))
	for _, job := range RubyCIConfigurationForSite(root, "rspec", "datadoghq.com").Jobs {
		require.Equal(t, "not checked", job.Status, job.Job+": "+job.Reason)
	}
}

func readFixtureFile(t *testing.T, root, path string) string {
	t.Helper()
	data, err := readSetupEdit(root, path)
	require.NoError(t, err)
	return string(data.before)
}

// CircleCI's Ruby orb is modeled from its source: install selects Ruby,
// install-deps runs bundle install, rspec-test runs RSpec as its script
// does and rubocop-check only lints.
func TestCircleCIRubyOrb(t *testing.T) {
	job := func(orb, steps string) string {
		return "version: 2.1\norbs:\n  ruby: circleci/ruby@" + orb + "\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\njobs:\n  rspec:\n    executor:\n      name: ruby/default\n      tag: \"3.2\"\n    parallelism: 2\n    steps:\n      - checkout\n      - ruby/install-deps\n      - ruby/rubocop-check\n" + steps + circleWorkflow("      - rspec\n")
	}
	const command = "bundle exec rspec --profile 10 --format RspecJunitFormatter --out /tmp/test-results/rspec/results.xml --format progress"
	for _, tc := range []struct{ name, orb, steps, want string }{
		{"2.x default", "2.5.3", "      - ruby/rspec-test\n", command},
		{"2.x order and tag", "2.5.3", "      - ruby/rspec-test:\n          order: rand\n          tag: ~slow\n", command + " --order rand --tag ~slow"},
		{"1.x order", "1.8.0", "      - ruby/rspec-test\n", command + " --order defined"},
		{"3.x tag list", "3.0.0", "      - ruby/rspec-test:\n          tag: fast ~slow\n", "<unresolved: CircleCI job main/rspec: ruby/rspec-test passes each tag of fast ~slow separately"},
		{"other include", "2.5.3", "      - ruby/rspec-test:\n          include: spec/models/**/*_spec.rb\n", "<unresolved: CircleCI job main/rspec: ruby/rspec-test runs the files matching spec/models/**/*_spec.rb"},
		{"4.x test suite", "4.0.0", "      - ruby/rspec-test:\n          suite-name: unit\n", "<unresolved: CircleCI job main/rspec: the orb command ruby/rspec-test from circleci/ruby@4.0.0 runs steps ddtest cannot read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := circleRubyRepository(t, job(tc.orb, tc.steps))
			converted := circleJobs(t, root)["main/rspec"]
			require.Equal(t, "cimg/ruby:3.2", converted.Image)
			runs := circleRuns(converted)
			require.Equal(t, "bundle install", runs[0])
			require.True(t, strings.HasPrefix(runs[1], tc.want), runs[1])
		})
	}
	root := circleRubyRepository(t, job("2.5.3", circleRubyOrb+"      - ruby/rspec-test\n"))
	workflows, err := circleCI{}.readWorkflows(root)
	require.NoError(t, err)
	review := strings.Join(workflows[0].Review, "\n")
	require.Contains(t, review, "ruby/rubocop-check runs RuboCop, a linter outside the test path")
	require.Contains(t, review, "ruby/rspec-test splits spec/**/*_spec.rb across parallel containers by timing")
	require.True(t, rubyDiscovery{}.ciSetupConfigured(root, "rspec", "datadoghq.com"))
	scope, err := DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)

	// ruby/install selects the Ruby the tests run on a machine executor.
	root = circleRubyRepository(t, "version: 2.1\norbs:\n  ruby: circleci/ruby@2.1.0\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\njobs:\n  rspec:\n    machine:\n      image: ubuntu-2204:current\n    steps:\n      - checkout\n      - ruby/install:\n          version: 3.2.2\n      - ruby/install-deps:\n          gemfile: gemfiles/rails.gemfile\n"+circleRubyOrb+"      - run: bundle exec rspec\n"+circleWorkflow("      - rspec\n"))
	jobs := RubyCIConfigurationForSite(root, "rspec", "").Jobs
	require.Len(t, jobs, 2, "the unresolved Bundler configuration stays a finding")
	require.Equal(t, "bundle exec rspec", jobs[1].Command)
	require.Equal(t, "3.2.2", jobs[1].Runtime)
	steps := strings.Join(circleRuns(circleJobs(t, root)["main/rspec"]), "\n")
	require.Contains(t, steps, "ruby/install-deps configures Bundler to use gemfiles/rails.gemfile in later steps")
}

// ksylvest/omniai's GitHub workflows only build documentation (bundle exec
// yard doc) and merge Dependabot pull requests: they don't make GitHub
// Actions a provider to choose, so CircleCI, which runs the tests, is
// selected.
func TestOmniaiDocumentationWorkflowsAreNotTestProviders(t *testing.T) {
	root := copyFixture(t, "circleci-omniai")
	useCIProvider(t, "")
	selected, err := SelectCIProvider(root, "")
	require.NoError(t, err)
	require.Equal(t, "circleci", selected)
	useCIProvider(t, "github")
	for _, framework := range []string{"jest", "rspec"} {
		scope, err := DiscoverValidationScope(root, framework)
		require.NoError(t, err)
		require.Empty(t, scope.Unresolved, framework)
	}
}

// presidentbeef/brakeman's test-3-2 step exports a variable, runs rake and
// then moves the coverage report. Literal exports before exactly one test
// command are its environment, a RUBYOPT="$RUBYOPT ..." rake argument keeps
// the preload and adds its options, and artifact handling after it is
// outside the test path. Anything else stays unresolved.
func TestRubyStepsWithSetupAndArtifactCommands(t *testing.T) {
	root := copyFixture(t, "circleci-brakeman")
	const step = "# This triggers SimpleCov to generate a coverage.json file\nexport CC_TEST_REPORTER_ID=CC_TEST_REPORTER_ID\nbundle exec rake RUBYOPT=\"$RUBYOPT --enable-frozen-string-literal --debug-frozen-string-literal\"\nmkdir -p tmp/\nmv coverage/coverage.json tmp/coverage.json\n"
	resolution := resolveRubyStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: step}, "minitest")
	require.True(t, resolution.Matched, resolution.Reason)
	require.Len(t, resolution.Tests, 1)
	require.Equal(t, "bundle exec rake", resolution.Tests[0].Command)
	require.Equal(t, map[string]string{"CC_TEST_REPORTER_ID": "CC_TEST_REPORTER_ID", "RUBYOPT": "--enable-frozen-string-literal --debug-frozen-string-literal"}, resolution.Tests[0].Environment)
	require.Empty(t, rubyRakeOptionsProblem(step), "the argument keeps the preload")

	for _, unresolved := range []string{
		"bundle exec rake\nbundle exec rake test:integration",
		"export SEED=$(date +%s)\nbundle exec rake",
		"if [ -n \"$CI\" ]; then export A=1; fi\nbundle exec rake",
		"bundle exec rake\nbundle exec rubocop --autocorrect",
		"bundle exec rake\nrm -rf coverage",
		"bundle exec rake RUBYOPT=\"$RUBYOPT $EXTRA\"",
	} {
		resolution := resolveRubyStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: unresolved}, "minitest")
		require.False(t, resolution.Matched && resolution.Reason == "", unresolved)
	}
}

// ksylvest/omniai's CircleCI configuration: rspec through circleci/ruby's
// rspec-test on a three-Ruby matrix, with documentation, RuboCop, release
// and Qlty jobs that run no tests.
func TestCircleCIOmniaiFixture(t *testing.T) {
	root := copyFixture(t, "circleci-omniai")
	useCIProvider(t, "circleci")
	scope, err := DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "bundle exec rspec --profile 10 --format RspecJunitFormatter --out /tmp/test-results/rspec/results.xml --format progress", scope.Commands[0].Command)
	require.Len(t, scope.Commands[0].locations, 3)
	require.Contains(t, scope.Caveats, ".circleci/config.yml: CircleCI orbs that only notify, scan or upload coverage run no tests and are outside this validation: qltysh/qlty-orb@0.0.11.")
	request := rubyDiscovery{}.ciSetupRequest(root, "rspec", "datadoghq.com", circleCI{}.setupTerms())
	require.NotContains(t, strings.Join(request.Notes, "\n"), "These CI jobs run", "Ruby 3.2 and newer ship a recent RubyGems")
}

// After the fix the report suggests, brakeman's test-3-2 job runs the orb
// and extends RUBYOPT: the step's export and $RUBYOPT read pass discovery's
// shell checks, so it is one configuration ready to validate.
func TestCircleCIBrakemanAfterTheRubyOptionsFix(t *testing.T) {
	root := copyFixture(t, "circleci-brakeman")
	useCIProvider(t, "circleci")
	data := readFixtureFile(t, root, circleCIFile)
	data = strings.Replace(data, "orbs:\n  qlty: qltysh/qlty-orb@0.1\n", "orbs:\n  qlty: qltysh/qlty-orb@0.1\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\n", 1)
	data = strings.ReplaceAll(data, "bundle install\n", "bundle install\n      - test-optimization-circleci-orb/autoinstrument:\n          languages: ruby\n          ruby_tracer_version: 1.42.0\n")
	data = strings.Replace(data, "RUBYOPT='--enable-frozen-string-literal --debug-frozen-string-literal'", "RUBYOPT=\"$RUBYOPT --enable-frozen-string-literal --debug-frozen-string-literal\"", 1)
	writeFile(t, root, circleCIFile, data)
	scope, err := DiscoverValidationScope(root, "minitest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 2)
	entry := scope.Commands[1]
	require.Equal(t, "bundle exec rake", entry.Command)
	require.Empty(t, entry.UnvalidatedSetup)
	require.Equal(t, "CC_TEST_REPORTER_ID", entry.Environment["CC_TEST_REPORTER_ID"])
	require.Equal(t, "--enable-frozen-string-literal --debug-frozen-string-literal", entry.Environment["RUBYOPT"])
	require.True(t, rubyDiscovery{}.ciSetupConfigured(root, "minitest", "datadoghq.com"))
}

// An installer under a condition ddtest cannot decide may or may not run,
// so the runtime it selects is unknown rather than certain.
func TestCircleCIConditionalInstallersLeaveTheRuntimeUnknown(t *testing.T) {
	root := circleRubyRepository(t, "version: 2.1\norbs:\n  ruby: circleci/ruby@2.1.0\n  node: circleci/node@5.2.0\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\njobs:\n  rspec:\n    docker:\n      - image: cimg/ruby:3.0\n    steps:\n      - checkout\n      - when:\n          condition:\n            equal: [ main, << pipeline.git.branch >> ]\n          steps:\n            - ruby/install:\n                version: \"3.3.6\"\n            - node/install:\n                node-version: \"16.20.2\"\n"+circleRubyOrb+"      - run: bundle exec rspec\n"+circleWorkflow("      - rspec\n"))
	job := circleJobs(t, root)["main/rspec"]
	require.Equal(t, map[string]string{"ruby": ""}, job.Steps[0].Runtime)
	require.Equal(t, map[string]string{"js": ""}, job.Steps[1].Runtime)
	jobs := RubyCIConfigurationForSite(root, "rspec", "").Jobs
	require.Len(t, jobs, 1)
	require.Empty(t, jobs[0].Runtime, "neither 3.0 nor 3.3 is certain")
	require.Equal(t, "inconclusive", jobs[0].Status)
}

// ksylvest/omniai's gemspec lists its files with an allowlist of globs
// (Dir.glob("{bin,lib,exe}/**/*") + %w[README.md Gemfile]) that cannot match
// .testoptimization/, so no gemspec edit is needed; a glob that could match
// it still asks for the exclusion.
func TestGemPackagingAllowlistsNeedNoEdit(t *testing.T) {
	root := copyFixture(t, "circleci-omniai")
	t.Chdir(root)
	text := providerSetupInstructions(root, circleCI{}, "ruby", "rspec", "", true)
	require.Contains(t, text, "The gemspec lists its files from specific paths that cannot include .testoptimization/, so the reports stay out of the gem: do not edit the gemspec.")
	for _, files := range []string{`Dir["**/*"]`, `Dir.glob("{lib,.testoptimization}/**/*")`, `Dir.glob("*")`, `Dir.glob("lib/**/*") + Dir.glob(".*")`, `Dir.glob("lib/**/*").reject { |f| f.end_with?(".tmp") }`} {
		writeFile(t, root, "omniai.gemspec", "Gem::Specification.new do |spec|\n  spec.files = "+files+"\nend\n")
		require.Contains(t, providerSetupInstructions(root, circleCI{}, "ruby", "rspec", "", true), "exclude .testoptimization/ from its files", files)
	}
}

// When a test command passes RUBYOPT as a rake argument, as brakeman's
// test-3-2 does, the handoff prescribes the inline RUBYOPT="$RUBYOPT ..."
// form for it on every provider; job-level options keep the re-add after
// the setup.
func TestRubyHandoffOffersTheInlineRakeOptions(t *testing.T) {
	root := copyFixture(t, "circleci-brakeman")
	t.Chdir(root)
	useCIProvider(t, "circleci")
	const want = `pass RUBYOPT as a rake argument, which replaces the preload for the tests rake runs: .circleci/config.yml / tests/test-3-2. Write that argument as RUBYOPT="$RUBYOPT <its options>", here RUBYOPT="$RUBYOPT --enable-frozen-string-literal --debug-frozen-string-literal"`
	require.Contains(t, providerSetupInstructions(root, circleCI{}, "ruby", "minitest", "", true), want)
	writeFile(t, root, ".gitlab-ci.yml", "test:\n  image: ruby:3.3\n  script:\n    - bundle exec rake RUBYOPT='-W0'\n")
	require.NoError(t, os.RemoveAll(filepath.Join(root, ".circleci")))
	useCIProvider(t, "gitlab")
	require.Contains(t, providerSetupInstructions(root, gitLabCI{}, "ruby", "minitest", "", true), `Write that argument as RUBYOPT="$RUBYOPT <its options>", here RUBYOPT="$RUBYOPT -W0"`)
	writeFile(t, root, ".gitlab-ci.yml", "test:\n  image: ruby:3.3\n  script:\n    - bundle exec rake\n")
	require.NotContains(t, providerSetupInstructions(root, gitLabCI{}, "ruby", "minitest", "", true), "as a rake argument")
}

// A rake RUBYOPT="$RUBYOPT -W2" argument extends what the job already
// exports, such as the Minitest fork initializer: the configuration keeps
// those options and adds -W2, while validation owns the preload.
func TestRakeRubyOptionsKeepInheritedOptions(t *testing.T) {
	root := circleRubyRepository(t, circleRubyJob("cimg/ruby:3.3", "", "", "      - run: echo 'export RUBYOPT=\"$RUBYOPT -r./ci/datadog_test_optimization.rb\"' >> \"$BASH_ENV\"\n"))
	data := readFixtureFile(t, root, circleCIFile)
	writeFile(t, root, circleCIFile, strings.Replace(data, "      - run: bundle exec rspec\n", "      - run: bundle exec rake RUBYOPT=\"$RUBYOPT -W2\"\n", 1))
	scope, err := DiscoverValidationScope(root, "rspec")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "bundle exec rake", scope.Commands[0].Command)
	require.Equal(t, "-r./ci/datadog_test_optimization.rb -W2", scope.Commands[0].Environment["RUBYOPT"])
}
