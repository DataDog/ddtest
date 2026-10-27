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

	"go.yaml.in/yaml/v3"
)

func readBuildkite(root, path string) (ciWorkflow, error) {
	w := ciWorkflow{Path: path, Provider: "buildkite", Jobs: map[string]runtimeJob{}}
	document, err := readCIYAML(root, path)
	if err != nil {
		return w, err
	}
	fields := ciMembers(document)
	var walk func(*yaml.Node, string, int)
	walk = func(list *yaml.Node, group string, depth int) {
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
			if nested := step["steps"]; nested != nil {
				walk(nested, group+name+" / ", depth+1)
				continue
			}
			node := step["command"]
			if node == nil {
				node = step["commands"]
			}
			if node == nil {
				if step["trigger"] != nil {
					w.Unresolved = append(w.Unresolved, path+" / "+group+name+": triggered pipeline requires its resolved test configuration")
				}
				continue
			}
			job := runtimeJob{Env: providerEnvironment("buildkite", mergeEnvironment(ciEnvironment(fields["env"]), ciEnvironment(step["env"])))}
			job.Steps = nativeSteps(path, node, "buildkite")
			if condition := ciScalar(step["if"]); condition != "" {
				if condition == "false" {
					job.If = "false"
				} else {
					w.Review = append(w.Review, path+" / "+group+name+": Buildkite condition is a possible CI path; event/branch eligibility is not validated")
				}
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
							if ciMembers(config)["environment"] != nil || ciMembers(config)["volumes"] != nil {
								job.Gaps = append(job.Gaps, "Docker plugin environment/volume forwarding needs review")
							}
						case "docker-compose":
							service := ciScalar(ciMembers(config)["run"])
							composePath := "docker-compose.yml"
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
								for _, line := range strings.Split(string(contents), "\n") {
									words := strings.Fields(line)
									if len(words) > 1 && strings.EqualFold(words[0], "FROM") {
										if from != "" {
											job.Gaps = append(job.Gaps, "Multi-stage Dockerfile requires explicit runtime selection")
										}
										from = words[1]
									}
								}
								job.Node, job.NodeSource = ciImageNode(from, job.Env), file+" / FROM "+from
							}
							job.Env = mergeEnvironment(job.Env, ciEnvironment(serviceFields["environment"]))
							if serviceFields["volumes"] != nil || serviceFields["depends_on"] != nil || serviceFields["entrypoint"] != nil {
								job.Gaps = append(job.Gaps, "Compose volumes, dependencies or entrypoint require local setup facts")
							}
						case "artifacts":
							w.Review = append(w.Review, path+" / "+group+name+": artifact transfer is not exercised locally")
						default:
							job.Gaps = append(job.Gaps, "Buildkite plugin "+pluginName+" needs its resolved runtime/setup; no plugin hooks were executed")
						}
					}
				}
			}
			if job.Node == "" && fields["image"] != nil {
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
				if strings.Contains(command.Run, "buildkite-agent pipeline upload") {
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
	walk(fields["steps"], "", 0)
	// Keep a stable ordering when multiple files/steps expose identical commands.
	for name, job := range w.Jobs {
		job.Gaps = slices.Compact(job.Gaps)
		job.Env = maps.Clone(job.Env)
		w.Jobs[name] = job
	}
	return w, nil
}
