// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// A hand-written datadog-ci setup uses the datadog-ci release the bundle
// locks, as datadog-ci's documentation shows: RUBYOPT=-rdatadog/ci/auto_instrument
// with DD_CIVISIBILITY_ENABLED=true in the job's environment, or an RSpec
// helper that .rspec loads, which requires datadog/ci and enables it in a
// Datadog.configure block.

// rubyHandWrittenHelper is the Datadog configuration a test helper writes.
type rubyHandWrittenHelper struct {
	// file is the helper's repository path.
	file string
	// enabled and instrumented report the block's c.ci.enabled = true and
	// c.ci.instrument :<framework>.
	enabled, instrumented bool
	// reporting is the block's c.ci.agentless_mode_enabled, true or false,
	// which replaces DD_CIVISIBILITY_AGENTLESS_ENABLED.
	reporting handWrittenReporting
	// unresolved is why ddtest cannot evaluate the helper statically.
	unresolved string
}

var (
	rubyRequireDatadogCI = regexp.MustCompile(`^(\s*)require\s*\(?\s*["']datadog/ci["']\s*\)?\s*(.*)$`)
	rubyDatadogConfigure = regexp.MustCompile(`^(\s*)Datadog\.configure\s*(?:do\s*\|\s*([a-z_][a-zA-Z0-9_]*)\s*\||\{\s*\|\s*([a-z_][a-zA-Z0-9_]*)\s*\|)`)
	rubyRSpecRequire     = regexp.MustCompile(`^\s*(?:--require|-r)[=\s]+(\S+)\s*$`)
)

func (rubyDiscovery) handWrittenSetup(root string, provider ciProvider, workflow ciWorkflow, job runtimeJob, index int, row map[string]any, framework, site string) *handWrittenSetup {
	step := job.Steps[index]
	env := handWrittenEnvironment(workflow, job, step)
	directory := filepath.Clean(filepath.FromSlash(stepDirectory(workflow, job, step)))
	if strings.HasPrefix(directory, "..") || filepath.IsAbs(directory) || strings.Contains(directory, "$") {
		return nil
	}
	options, set := env["RUBYOPT"]
	auto := set && strings.Contains(options, "datadog/ci")
	var helper *rubyHandWrittenHelper
	if !auto {
		if helper = rubyHandWrittenHelperFor(root, directory, framework, step.Run); helper == nil {
			return nil
		}
	}
	mechanism := "RUBYOPT"
	if helper != nil {
		mechanism = helper.file
	}
	setup := &handWrittenSetup{Reference: handWrittenReference("datadog-ci", mechanism)}
	enabled, reporting := false, reportingFromEnvironment
	if auto {
		words := strings.Fields(options)
		preload := slices.IndexFunc(words, func(word string) bool {
			return word == "-rdatadog/ci/auto_instrument" || word == "datadog/ci/auto_instrument"
		})
		_, dynamic := handWrittenReads(options)
		switch {
		case dynamic:
			setup.unresolved("RUBYOPT (" + options + ") holds values only CI knows, so ddtest cannot tell what it preloads.")
		case preload < 0:
			setup.missing("RUBYOPT loads datadog/ci without its auto instrumentation: preload -rdatadog/ci/auto_instrument instead, which starts Test Optimization for " + framework + ".")
		case !slices.Contains(words[:preload], "-rbundler/setup") && !slices.Contains(words[:preload], "bundler/setup"):
			// Ruby reads RUBYOPT when it starts bundle exec or a binstub too,
			// before either sets up the bundle.
			setup.missing("RUBYOPT preloads datadog/ci/auto_instrument before Bundler sets up the bundle, so Ruby cannot load the datadog-ci it locks (it reads RUBYOPT when starting bundle exec or a binstub such as bin/rspec too): preload -rbundler/setup before it, as in RUBYOPT=\"-rbundler/setup -rdatadog/ci/auto_instrument\".")
		}
		handWrittenPreloadBeforeInstall(setup, workflow, job, index, "RUBYOPT", "datadog-ci", rubyInstallsBundle, func(value string) bool {
			return strings.Contains(value, "datadog/ci")
		})
	} else {
		enabled, reporting = helper.enabled, helper.reporting
		switch {
		case helper.unresolved != "":
			setup.unresolved(helper.unresolved)
		case !helper.instrumented:
			setup.missing(helper.file + " configures datadog-ci without instrumenting " + framework + ": add c.ci.instrument :" + framework + " to its Datadog.configure block.")
		}
	}
	lockfile := rubyHandWrittenLockfile(root, directory, env)
	if lockfile == "" {
		setup.unresolved("BUNDLE_GEMFILE (" + env["BUNDLE_GEMFILE"] + ") selects a bundle outside the repository ddtest reads.")
	} else if setup.Tracer = rubyLockedRelease(filepath.Join(root, filepath.FromSlash(lockfile))); setup.Tracer != "" {
		setup.Pin = lockfile
		setup.Reference = handWrittenReference("datadog-ci "+setup.Tracer, mechanism)
	} else {
		setup.missing(lockfile + " does not lock datadog-ci, so the tests cannot load the release ddtest validates: add gem \"datadog-ci\", \"<exact release>\" (such as 1.42.0) to the test group of its Gemfile and run bundle lock.")
	}
	handWrittenFrameworkDisabled(setup, env, framework)
	handWrittenConfiguration(setup, provider, job, env, site, enabled, reporting)
	return setup
}

// rubyInstallsBundle reports a command that installs the bundle or a gem:
// bundle install (bundle alone, bundle update) or gem install.
func rubyInstallsBundle(words []string) bool {
	switch {
	case len(words) == 0:
		return false
	case words[0] == "bundle":
		return len(words) == 1 || strings.HasPrefix(words[1], "-") || slices.Contains([]string{"install", "i", "update"}, words[1])
	default:
		return words[0] == "gem" && len(words) > 1 && words[1] == "install"
	}
}

// rubyHandWrittenLockfile is the repository path of the Gemfile.lock the
// test command in directory uses: BUNDLE_GEMFILE's, otherwise the nearest
// Gemfile.lock at or above directory; empty for a Gemfile outside the
// repository.
func rubyHandWrittenLockfile(root, directory string, env map[string]string) string {
	if gemfile, set := env["BUNDLE_GEMFILE"]; set {
		gemfile = strings.TrimSpace(gemfile)
		if relative, ok := strings.CutPrefix(gemfile, ciProjectDirectory+"/"); ok {
			gemfile = relative
		} else {
			gemfile = path.Join(filepath.ToSlash(directory), gemfile)
		}
		gemfile = path.Clean(gemfile)
		if strings.ContainsAny(gemfile, "$`") || strings.HasPrefix(gemfile, "..") || path.IsAbs(gemfile) {
			return ""
		}
		return gemfile + ".lock"
	}
	for current := directory; ; current = filepath.Dir(current) {
		if _, err := os.Stat(filepath.Join(root, current, "Gemfile.lock")); err == nil || current == "." || current == "" {
			return path.Join(filepath.ToSlash(current), "Gemfile.lock")
		}
	}
}

// rubyLockedRelease is the datadog-ci release a Gemfile.lock locks, or empty.
func rubyLockedRelease(lockfile string) string {
	data, err := os.ReadFile(lockfile)
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if version, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "    datadog-ci ("); ok {
			version = strings.TrimSuffix(version, ")")
			if exactRubyRelease.MatchString(version) {
				return version
			}
		}
	}
	return ""
}

// rubyHandWrittenHelperFor reads the helper an RSpec run in directory
// (command) loads through its options file (--require <helper>) when it
// sets up datadog-ci, or a Minitest test/test_helper.rb that does, which
// ddtest cannot tell every test file loads. The options file is .rspec, or
// the one the command selects with --options, which replaces it. A path
// written from the working directory (./spec/spec_helper.rb) is read from
// there, a bare name (spec_helper) from spec/ or lib/, RSpec's load path. nil
// when none sets up datadog-ci.
func rubyHandWrittenHelperFor(root, directory, framework, command string) *rubyHandWrittenHelper {
	var candidates []string
	if framework == "rspec" {
		options, ok := rubyRSpecOptionsFile(command)
		if !ok {
			return nil
		}
		data, err := os.ReadFile(filepath.Join(root, directory, filepath.FromSlash(options)))
		if err != nil {
			return nil
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			match := rubyRSpecRequire.FindStringSubmatch(line)
			if match == nil {
				continue
			}
			name := strings.TrimSuffix(match[1], ".rb") + ".rb"
			switch {
			case path.IsAbs(name):
			case strings.HasPrefix(name, "./") || strings.HasPrefix(name, "../"):
				candidates = append(candidates, path.Join(filepath.ToSlash(directory), name))
			default:
				candidates = append(candidates, path.Join(filepath.ToSlash(directory), "spec", name), path.Join(filepath.ToSlash(directory), "lib", name))
			}
		}
	} else {
		candidates = []string{path.Join(filepath.ToSlash(directory), "test", "test_helper.rb")}
	}
	for _, file := range candidates {
		if strings.HasPrefix(file, "../") {
			continue // Outside the repository ddtest reads.
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			continue
		}
		helper := rubyReadHelper(string(data), framework)
		if helper == nil {
			continue
		}
		helper.file = file
		if framework != "rspec" && helper.unresolved == "" {
			helper.unresolved = file + " sets up datadog-ci, and no Minitest configuration shows that every test file loads it."
		}
		return helper
	}
	return nil
}

// rubyRSpecOptionsFile is the options file an RSpec command reads, relative
// to its working directory: the one --options (-O) selects, otherwise
// .rspec. ok is false for one outside the repository ddtest reads.
func rubyRSpecOptionsFile(command string) (file string, ok bool) {
	file = ".rspec"
	commands, err := staticCommands(command)
	if err != nil {
		return file, true
	}
	for _, words := range commands {
		if !slices.ContainsFunc(words, func(word string) bool { return path.Base(word) == "rspec" }) {
			continue // Such as curl -O.
		}
		for i, word := range words {
			option, value, inline := strings.Cut(word, "=")
			if option != "--options" && option != "-O" {
				continue
			}
			if !inline {
				if i+1 >= len(words) {
					continue
				}
				value = words[i+1]
			}
			if path.IsAbs(value) || strings.HasPrefix(path.Clean(value), "..") {
				return "", false
			}
			file = value
		}
	}
	return file, true
}

// rubyReadHelper reads a helper's top-level require "datadog/ci" and
// Datadog.configure block. A require or block inside other code, or a
// setting with a value or condition ddtest does not evaluate, leaves the
// helper unresolved. nil when the helper does not require datadog/ci.
func rubyReadHelper(text, framework string) *rubyHandWrittenHelper {
	lines := strings.Split(text, "\n")
	var helper *rubyHandWrittenHelper
	for i, line := range lines {
		match := rubyRequireDatadogCI.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		helper = &rubyHandWrittenHelper{}
		if match[1] != "" || strings.TrimSpace(match[2]) != "" && !strings.HasPrefix(strings.TrimSpace(match[2]), "#") {
			helper.unresolved = "the helper requires datadog/ci inside a condition or other code (line " + strconv.Itoa(i+1) + "), which ddtest does not evaluate."
		}
		break
	}
	if helper == nil {
		return nil
	}
	start, variable := -1, ""
	for i, line := range lines {
		if match := rubyDatadogConfigure.FindStringSubmatch(line); match != nil {
			if match[1] != "" && helper.unresolved == "" {
				helper.unresolved = "the helper's Datadog.configure block is inside other code (line " + strconv.Itoa(i+1) + "), which ddtest does not evaluate."
			}
			start, variable = i, match[2]+match[3]
			break
		}
	}
	if start < 0 {
		if helper.unresolved == "" {
			helper.unresolved = "the helper requires datadog/ci without a Datadog.configure do |c| block ddtest reads, so ddtest cannot tell how it is configured."
		}
		return helper
	}
	setting := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(variable) + `\.ci\.([a-z_]+)\s*(=\s*)?(.*?)\s*$`)
	for i, line := range lines[start+1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "end" || trimmed == "}" {
			break
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		match := setting.FindStringSubmatch(line)
		value := ""
		if match != nil {
			value, _, _ = strings.Cut(match[3], " #")
			value = strings.TrimSpace(value)
		}
		switch {
		case match == nil && !strings.HasPrefix(trimmed, variable+"."):
			if helper.unresolved == "" {
				helper.unresolved = "the helper's Datadog.configure block runs code ddtest does not evaluate (line " + strconv.Itoa(start+i+2) + ": " + trimmed + ")."
			}
		case match == nil && !rubyHelperSettingIgnored(strings.TrimPrefix(trimmed, variable+".")):
			// Such as c.site = "datadoghq.eu" or c.api_key = ...: where and
			// whether the tracer reports.
			if helper.unresolved == "" {
				helper.unresolved = "the helper sets " + trimmed + " (line " + strconv.Itoa(start+i+2) + "), which ddtest does not evaluate."
			}
		case match == nil:
		case match[1] == "enabled" && match[2] != "":
			if value == "true" {
				helper.enabled = true
			} else if helper.unresolved == "" {
				helper.unresolved = "the helper sets " + variable + ".ci.enabled = " + value + ", which ddtest does not evaluate."
			}
		case match[1] == "agentless_mode_enabled" && match[2] != "":
			switch {
			case value == "true":
				helper.reporting = reportingAgentless
			case value == "false":
				helper.reporting = reportingAgent
			case helper.unresolved == "":
				helper.unresolved = "the helper sets " + variable + ".ci.agentless_mode_enabled = " + value + ", which ddtest does not evaluate."
			}
		case match[1] == "instrument" && match[2] == "":
			name := strings.TrimPrefix(strings.Fields(value + " ")[0], ":")
			name = strings.Trim(strings.TrimSuffix(name, ","), `"'`)
			if name != framework {
				break
			}
			// Options or a trailing condition, such as
			// if ENV["TRACE_TESTS"] == "true", can leave it off.
			if value == ":"+framework || value == `"`+framework+`"` || value == "'"+framework+"'" {
				helper.instrumented = true
			} else if helper.unresolved == "" {
				helper.unresolved = "the helper calls " + variable + ".ci.instrument " + value + ", which ddtest does not evaluate."
			}
		}
	}
	return helper
}

// rubyHelperSettingIgnored reports a Datadog.configure setting outside
// c.ci that changes neither where nor whether the tracer reports, such as
// service = "app".
func rubyHelperSettingIgnored(setting string) bool {
	name, _, _ := strings.Cut(setting, "=")
	name = strings.TrimSpace(name)
	return slices.Contains([]string{"service", "env", "version", "tags"}, name)
}
