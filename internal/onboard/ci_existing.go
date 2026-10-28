// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kballard/go-shellquote"
)

var nativeInitializationChannels = []struct{ variable, framework string }{
	{"NODE_OPTIONS", ""},
	{"DD_CUCUMBER_NODE_OPTIONS", "cucumber"},
	{"DD_WDIO_NODE_OPTIONS", "webdriverio"},
}

// Recognize ordinary package-based CI initialization as well as ddtest's own
// recipe. A literal preload alone does not prove that CI installs that package.
func nativeExistingSetup(root, provider string, job runtimeJob, index int) *nativeCommand {
	step := job.Steps[index]
	if step.Native == nil || step.Native.Tracer != "" {
		return step.Native
	}
	env := mergeEnvironment(job.Env, step.Env)
	if words, err := shellquote.Split(step.Run); err == nil {
		if _, assignments, err := commandEnvironment(words); err == nil {
			if value, ok := assignments["NODE_OPTIONS"]; ok {
				env["NODE_OPTIONS"] = value
			}
		}
	}
	count, framework, esm := 0, "", false
	for _, channel := range nativeInitializationChannels {
		if preload, imports := nativePreloads(env[channel.variable]); preload {
			count++
			framework, esm = channel.framework, imports
		}
	}
	if count != 1 {
		return step.Native
	}
	directory, err := repositoryDirectory(root, root, stepDirectory(ciWorkflow{}, job, step))
	if err != nil {
		return step.Native
	}
	var pkg struct{ Dependencies, DevDependencies map[string]string }
	data, err := os.ReadFile(filepath.Join(directory, "package.json"))
	if err != nil || json.Unmarshal(data, &pkg) != nil {
		return step.Native
	}
	version := pkg.Dependencies["dd-trace"]
	if version == "" {
		version = pkg.DevDependencies["dd-trace"]
	}
	if !exactRelease(version) || !nativePackageInstalledBefore(root, provider, job, index, directory, pkg.Dependencies["dd-trace"] == "") {
		return step.Native
	}
	verified := *step.Native
	verified.Tracer = version
	verified.Manual = true
	verified.ESM = esm
	verified.Framework = framework
	return &verified
}

func nativePreloads(options string) (bool, bool) {
	words, err := shellquote.Split(options)
	if err != nil {
		return false, false
	}
	preload, esm := false, false
	for i := 0; i < len(words); i++ {
		flag, module, equals := strings.Cut(words[i], "=")
		if !slices.Contains([]string{"-r", "--require", "--import"}, flag) {
			continue
		}
		if !equals {
			if i+1 >= len(words) {
				return false, false
			}
			i++
			module = words[i]
		}
		if (flag == "-r" || flag == "--require") && slices.Contains([]string{"dd-trace/ci/init", "dd-trace/ci/init.js"}, module) {
			preload = true
		}
		if flag == "--import" && module == "dd-trace/register.js" {
			esm = true
		}
	}
	return preload, esm
}

func nativePackageInstalledBefore(root, provider string, job runtimeJob, index int, directory string, development bool) bool {
	installed := false
	for _, step := range job.Steps[:index] {
		if provider == "jenkins" && (step.Native == nil || job.Steps[index].Native == nil || step.Native.Workspace == "" || step.Native.Workspace != job.Steps[index].Native.Workspace) {
			continue
		}
		preload, _ := nativePreloads(mergeEnvironment(job.Env, step.Env)["NODE_OPTIONS"])
		source, err := repositoryDirectory(root, root, stepDirectory(ciWorkflow{}, job, step))
		if err != nil || source != directory {
			continue
		}
		commands, err := staticCommands(step.Run)
		if err != nil {
			continue
		}
		installEnv := mergeEnvironment(job.Env, step.Env)
		nodeEnv := installEnv["NODE_ENV"]
		if provider == "gitlab" || provider == "buildkite" {
			if literal, ok := step.LiteralEnv["NODE_ENV"]; !ok || literal != nodeEnv {
				if nodeEnv, err = nativeEnvironmentValue(nodeEnv, installEnv); err != nil && development {
					continue
				}
			}
		}
		for _, words := range commands {
			nodeEnv := nodeEnv
			commandEnv := installEnv
			if normalized, assignments, err := commandEnvironment(words); err == nil {
				words = normalized
				commandEnv = mergeEnvironment(installEnv, assignments)
				if value, ok := assignments["NODE_ENV"]; ok {
					nodeEnv = value
				}
			}
			if len(words) == 0 {
				continue
			}
			if words[0] == "cd" {
				return false // Starting-directory evidence cannot follow this shell state.
			}
			if len(words) > 1 && slices.Contains([]string{"npm", "yarn", "pnpm"}, words[0]) && slices.Contains([]string{"install", "i", "add", "update", "up", "upgrade", "remove", "rm", "uninstall"}, words[1]) && slices.ContainsFunc(words[2:], func(word string) bool {
				return word == "dd-trace" || strings.HasPrefix(word, "dd-trace@")
			}) {
				// Package overrides or removals invalidate the static manifest's
				// pin, even if an earlier command installed that manifest.
				return false
			}
			prune := len(words) > 1 && slices.Contains([]string{"npm", "pnpm"}, words[0]) && words[1] == "prune"
			bareYarn := words[0] == "yarn" && len(words) == 1
			if !prune && !bareYarn && (len(words) < 2 || !slices.Contains([]string{"npm", "yarn", "pnpm"}, words[0]) || !slices.Contains([]string{"ci", "install", "i"}, words[1])) {
				continue
			}
			omitsDev := nodeEnv == "production"
			includesDev, noInstall := false, false
			if words[0] == "npm" {
				omit, include, skip, known := nativeNpmInstallConfig(provider, commandEnv, step.LiteralEnv)
				if !known {
					return false
				}
				omitsDev, includesDev, noInstall = omitsDev || omit, include, skip
			}
			for i, word := range words {
				omitsDev = omitsDev || slices.Contains([]string{"--prod", "--prod=true", "--production", "--production=true", "--only=production", "--omit=dev"}, word) ||
					(word == "--omit" && i+1 < len(words) && words[i+1] == "dev") || words[0] == "pnpm" && word == "-P"
				includesDev = includesDev || word == "--include=dev" || word == "--include" && i+1 < len(words) && words[i+1] == "dev"
				flag, value, _ := strings.Cut(word, "=")
				noInstall = noInstall || slices.Contains([]string{"--prefix", "--cwd", "--dir", "--workspace", "-w", "--workspaces", "--filter", "-F"}, flag) || slices.Contains([]string{"--package-lock-only", "--lockfile-only", "--dry-run", "-g", "--global"}, flag) && value != "false"
			}
			if development && omitsDev && !includesDev && !noInstall {
				installed = false
				continue
			}
			if prune || preload || noInstall || slices.Contains(words, "--prefix") || slices.Contains(words, "-g") {
				continue
			}
			installed = true
		}
	}
	return installed
}

// Read only npm options that affect evidence of a local tracer installation.
// npm accepts either prefix case, with lowercase taking precedence.
func nativeNpmInstallConfig(provider string, env, literals map[string]string) (omit, include, skip, known bool) {
	values := map[string]string{}
	for _, key := range slices.Sorted(maps.Keys(env)) {
		name := strings.TrimPrefix(strings.ToLower(key), "npm_config_")
		if name == strings.ToLower(key) || !slices.Contains([]string{"omit", "include", "production", "only", "dry_run", "package_lock_only", "global", "prefix", "workspace", "workspaces"}, name) {
			continue
		}
		value := env[key]
		if (provider == "gitlab" || provider == "buildkite") && literals[key] != value {
			var err error
			value, err = nativeEnvironmentValue(value, env)
			if err != nil {
				return false, false, false, false
			}
		}
		values[name] = strings.ToLower(value)
	}
	omit = slices.Contains(strings.Fields(values["omit"]), "dev") || values["production"] == "true" || slices.Contains([]string{"prod", "production"}, values["only"])
	include = slices.Contains(strings.Fields(values["include"]), "dev")
	for _, key := range []string{"dry_run", "package_lock_only", "global", "prefix", "workspace", "workspaces"} {
		skip = skip || values[key] != "" && values[key] != "false"
	}
	return omit, include, skip, true
}
