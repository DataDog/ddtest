// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/kballard/go-shellquote"
	"go.yaml.in/yaml/v3"
)

func readBuildkite(root, path string) (ciWorkflow, error) {
	w := ciWorkflow{Path: path, Provider: "buildkite", Jobs: map[string]runtimeJob{}}
	document, err := readCIYAML(root, path)
	if err != nil {
		return w, err
	}
	fields := ciMembers(document)
	var walk func(*yaml.Node, string, int, bool)
	walk = func(list *yaml.Node, group string, depth int, excluded bool) {
		list = ciUnalias(list)
		if list == nil {
			return
		}
		if depth > 16 {
			w.Unresolved = append(w.Unresolved, path+": step groups exceed 16 levels")
			return
		}
		for index, entry := range list.Content {
			step := ciMembers(entry)
			if len(step) == 0 {
				continue
			}
			name := ciScalar(step["key"])
			if name == "" {
				name = ciScalar(step["label"])
			}
			if name == "" {
				name = ciScalar(step["name"])
			}
			if name == "" {
				name = "step " + strconv.Itoa(index+1)
			}
			disabled := excluded || ciScalar(step["if"]) == "false"
			if skip := ciUnalias(step["skip"]); skip != nil {
				disabled = disabled || skip.Tag == "!!bool" && strings.EqualFold(skip.Value, "true") || skip.Tag == "!!str" && skip.Value != ""
			}
			if nested := step["steps"]; nested != nil {
				if !disabled && ciScalar(step["if"]) != "" {
					w.Review = append(w.Review, path+" / "+group+name+": Buildkite group condition is a possible CI path; event/branch eligibility is not validated")
				}
				walk(nested, group+name+" / ", depth+1, disabled)
				continue
			}
			node := step["command"]
			if node == nil {
				node = step["commands"]
			}
			steps := buildkitePluginCommands(path, step["plugins"], node != nil)
			if len(steps) == 0 {
				steps = nativeSteps(path, node, "buildkite")
			}
			if len(steps) == 0 {
				if !disabled && step["trigger"] != nil {
					w.Unresolved = append(w.Unresolved, path+" / "+group+name+": triggered pipeline requires its resolved test configuration")
				} else if !disabled && step["plugins"] != nil {
					w.Unresolved = append(w.Unresolved, path+" / "+group+name+": commandless plugin step requires its resolved command and environment")
				}
				continue
			}
			job := runtimeJob{Env: providerEnvironment("buildkite", mergeEnvironment(ciEnvironment(fields["env"]), ciEnvironment(step["env"])))}
			job.Steps = steps
			for _, step := range steps {
				if step.Native != nil && step.Native.Blocked != "" {
					job.Gaps = append(job.Gaps, step.Native.Blocked)
				}
			}
			if disabled {
				job.If = "false"
			} else if condition := ciScalar(step["if"]); condition != "" {
				w.Review = append(w.Review, path+" / "+group+name+": Buildkite condition is a possible CI path; event/branch eligibility is not validated")
			}
			plugins := step["plugins"]
			if plugins != nil {
				for _, plugin := range plugins.Content {
					if plugin.Kind != yaml.MappingNode {
						job.Gaps = append(job.Gaps, "Buildkite plugin "+ciScalar(plugin)+" needs its resolved implementation and environment")
						continue
					}
					for pluginName, config := range ciMembers(plugin) {
						kind, _, _ := strings.Cut(pluginName, "#")
						switch kind {
						case "docker":
							image := ciScalar(ciMembers(config)["image"])
							job.Node, job.NodeSource = ciImageNode(image, job.Env), "Buildkite Docker image "+image
							if mount := ciMembers(config)["mount-checkout"]; mount != nil && ciScalar(mount) != "true" {
								job.Gaps = append(job.Gaps, "Docker plugin checkout mount is disabled or unresolved; supply an image-to-repository mapping before validation or editing")
							}
							if ciMembers(config)["environment"] != nil || ciMembers(config)["volumes"] != nil {
								job.Gaps = append(job.Gaps, "Docker plugin environment/volume forwarding needs review")
							}
							if ciMembers(config)["shell"] != nil || ciMembers(config)["entrypoint"] != nil {
								job.Gaps = append(job.Gaps, "Docker plugin shell/entrypoint overrides require resolved execution semantics before validation or editing")
							}
						case "docker-compose":
							service := ciScalar(ciMembers(config)["run"])
							composePath := "docker-compose.yml"
							if files := ciUnalias(ciMembers(config)["config"]); files != nil && files.Kind != yaml.ScalarNode {
								job.Node, job.NodeSource = "", "unmerged Docker Compose configuration files"
								job.Gaps = append(job.Gaps, "Docker Compose config array requires its merged service configuration before validation or editing")
								continue
							}
							if configured := ciScalar(ciMembers(config)["config"]); configured != "" {
								composePath = configured
							}
							compose, e := readCIYAML(root, composePath)
							if e != nil {
								job.Gaps = append(job.Gaps, "Docker Compose service configuration is unavailable: "+e.Error())
								continue
							}
							serviceFields := ciMembers(ciMembers(ciMembers(compose)["services"])[service])
							if len(serviceFields) == 0 {
								job.Gaps = append(job.Gaps, "Docker Compose service "+service+" is not defined")
								continue
							}
							image := ciScalar(serviceFields["image"])
							job.Node, job.NodeSource = ciImageNode(image, job.Env), composePath+" / "+service+" / image"
							if build := ciUnalias(serviceFields["build"]); build != nil && build.Kind == yaml.MappingNode {
								job.Node = ""
								job.Gaps = append(job.Gaps, "Docker Compose build mapping requires its resolved context and Dockerfile")
							}
							if build := ciScalar(serviceFields["build"]); build != "" {
								if !filepath.IsLocal(build) {
									job.Gaps = append(job.Gaps, "Docker build context is not repository-relative")
									continue
								}
								file := filepath.ToSlash(filepath.Join(build, "Dockerfile"))
								if _, ok := repositoryFile(root, root, file); !ok {
									job.Gaps = append(job.Gaps, "Dockerfile unavailable inside repository")
									continue
								}
								contents, e := os.ReadFile(filepath.Join(root, file))
								if e != nil {
									job.Gaps = append(job.Gaps, e.Error())
									continue
								}
								from := ""
								changedRuntime := false
								for _, line := range strings.Split(string(contents), "\n") {
									words := strings.Fields(line)
									if len(words) > 1 && strings.EqualFold(words[0], "WORKDIR") {
										job.Gaps = append(job.Gaps, file+": Dockerfile working directory requires a resolved repository mapping before validation or editing")
									}
									if len(words) > 1 && strings.EqualFold(words[0], "FROM") {
										if from != "" {
											job.Gaps = append(job.Gaps, "Multi-stage Dockerfile requires explicit runtime selection")
										}
										from = words[1]
									}
									if len(words) > 1 && strings.EqualFold(words[0], "RUN") {
										command := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), words[0]))
										changedRuntime = changedRuntime || changesNodeRuntime(command) || strings.HasSuffix(command, "\\") || strings.HasPrefix(command, "[")
									}
									if len(words) > 1 && strings.EqualFold(words[0], "ENV") && (strings.Contains(" "+strings.Join(words[1:], " "), " PATH=") || words[1] == "PATH") {
										changedRuntime = true
									}
								}
								job.Node, job.NodeSource = ciImageNode(from, job.Env), file+" / FROM "+from
								if changedRuntime {
									job.Node = ""
									job.Gaps = append(job.Gaps, file+": Dockerfile changes or obscures the Node runtime after FROM; supply its actual runtime")
								}
							}
							job.Env = mergeEnvironment(job.Env, ciEnvironment(serviceFields["environment"]))
							if ciMembers(config)["env"] != nil || ciMembers(config)["environment"] != nil {
								job.Gaps = append(job.Gaps, "Docker Compose plugin environment overrides require resolved container values before validation or editing")
							}
							if ciMembers(config)["shell"] != nil || serviceFields["working_dir"] != nil || serviceFields["volumes"] != nil || serviceFields["depends_on"] != nil || serviceFields["entrypoint"] != nil {
								job.Gaps = append(job.Gaps, "Compose shell, working directory, volumes, dependencies or entrypoint require local setup facts")
							}
						case "artifacts":
							w.Review = append(w.Review, path+" / "+group+name+": artifact transfer is not exercised locally")
						default:
							job.Gaps = append(job.Gaps, "Buildkite plugin "+pluginName+" needs its resolved runtime/setup; no plugin hooks were executed")
						}
					}
				}
			}
			if job.Node == "" && job.NodeSource == "" && fields["image"] != nil {
				image := ciScalar(fields["image"])
				job.Node, job.NodeSource = ciImageNode(image, job.Env), "Buildkite image "+image
			}
			if step["matrix"] != nil {
				job.Gaps = append(job.Gaps, "Buildkite matrix needs explicit expanded commands and environment")
			}
			if step["depends_on"] != nil {
				w.Review = append(w.Review, path+" / "+group+name+": cross-step dependencies require available artifacts or explicit local preparation")
			}
			for _, command := range job.Steps {
				if !disabled && strings.Contains(command.Run, "buildkite-agent pipeline upload") {
					w.Unresolved = append(w.Unresolved, path+" / "+group+name+": dynamically uploaded pipeline must be supplied as configuration; discovery never executes generators")
				}
			}
			key := group + name
			if _, exists := w.Jobs[key]; exists {
				key += fmt.Sprintf(" [%d]", index+1)
			}
			w.Jobs[key] = nativeShellState(root, job)
		}
	}
	walk(fields["steps"], "", 0, false)
	// Keep a stable ordering when multiple files/steps expose identical commands.
	for name, job := range w.Jobs {
		job.Gaps = slices.Compact(job.Gaps)
		job.Env = maps.Clone(job.Env)
		w.Jobs[name] = job
	}
	return w, nil
}

// Plugin command arrays are container argv, not a shell script. Keep literal
// test consumers visible for provider choice, but require explicit placement
// instead of rewriting argv into a shell wrapper with different semantics.
func buildkitePluginCommands(path string, plugins *yaml.Node, hasStepCommand bool) []runtimeStep {
	plugins = ciUnalias(plugins)
	if plugins == nil {
		return nil
	}
	var steps []runtimeStep
	for _, plugin := range plugins.Content {
		for name, config := range ciMembers(plugin) {
			kind, _, _ := strings.Cut(name, "#")
			command := ciUnalias(ciMembers(config)["command"])
			if !slices.Contains([]string{"docker", "docker-compose"}, kind) || command == nil {
				continue
			}
			run := shellquote.Join(ciStrings(command)...)
			reason := "Buildkite " + kind + " plugin command uses container argv; supply explicitly resolved initialization and execution before validation or editing"
			if kind == "docker-compose" && hasStepCommand {
				reason = "Buildkite docker-compose plugin command conflicts with the step command; choose one explicitly before validation or editing"
			}
			steps = append(steps, runtimeStep{
				Run:             strings.ReplaceAll(run, "$$", "$"),
				ResolutionError: buildkiteUploadGap(run),
				Source:          fmt.Sprintf("%s:%d", path, command.Line),
				Number:          command.Line,
				Native:          &nativeCommand{Path: path, Original: run, Blocked: reason},
			})
		}
	}
	return steps
}

// Buildkite interpolates unescaped variables before step environments exist.
// Only doubled dollars preserve a reference for the runtime shell.
func buildkiteUploadGap(command string) string {
	for i := 0; i+1 < len(command); i++ {
		if command[i] != '$' {
			continue
		}
		next := command[i+1]
		if next == '$' {
			i++
			continue
		}
		if next == '{' || next == '_' || next >= 'A' && next <= 'Z' || next >= 'a' && next <= 'z' {
			return "Buildkite upload-time variables require resolved pipeline values before validation or editing; step env is runtime-only (use $$VAR if runtime expansion is intended)"
		}
	}
	return ""
}
