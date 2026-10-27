// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kballard/go-shellquote"
)

// Recognize ordinary package-based CI initialization as well as ddtest's own
// recipe. A literal preload alone does not prove that CI installs that package.
func nativeExistingSetup(root, provider string, job runtimeJob, index int) *nativeCommand {
	step := job.Steps[index]
	if step.Native == nil || step.Native.Tracer != "" {
		return step.Native
	}
	options := mergeEnvironment(job.Env, step.Env)["NODE_OPTIONS"]
	if words, err := shellquote.Split(step.Run); err == nil {
		if _, assignments, err := commandEnvironment(words); err == nil {
			if value, ok := assignments["NODE_OPTIONS"]; ok {
				options = value
			}
		}
	}
	preload, esm := nativePreloads(options)
	if !preload {
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
	for _, step := range job.Steps[:index] {
		if provider == "jenkins" && (step.Native == nil || job.Steps[index].Native == nil || step.Native.Workspace == "" || step.Native.Workspace != job.Steps[index].Native.Workspace) {
			continue
		}
		if preload, _ := nativePreloads(mergeEnvironment(job.Env, step.Env)["NODE_OPTIONS"]); preload {
			continue
		}
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
			if len(words) == 0 {
				continue
			}
			bareYarn := words[0] == "yarn" && len(words) == 1
			if !bareYarn && (len(words) < 2 || !slices.Contains([]string{"npm", "yarn", "pnpm"}, words[0]) || !slices.Contains([]string{"ci", "install", "i"}, words[1])) {
				continue
			}
			omitsDev := nodeEnv == "production"
			noInstall := false
			for i, word := range words {
				omitsDev = omitsDev || slices.Contains([]string{"--prod", "--prod=true", "--production", "--production=true", "--only=production", "--omit=dev"}, word) ||
					(word == "--omit" && i+1 < len(words) && words[i+1] == "dev") || words[0] == "pnpm" && word == "-P"
				flag, value, _ := strings.Cut(word, "=")
				noInstall = noInstall || slices.Contains([]string{"--prefix", "--cwd", "--dir", "--workspace", "-w", "--workspaces", "--filter", "-F"}, flag) || slices.Contains([]string{"--package-lock-only", "--lockfile-only", "--dry-run", "-g", "--global"}, flag) && value != "false"
			}
			if development && omitsDev || noInstall || slices.Contains(words, "--prefix") || slices.Contains(words, "-g") {
				continue
			}
			return true
		}
	}
	return false
}
