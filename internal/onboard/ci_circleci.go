// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/kballard/go-shellquote"
	"go.yaml.in/yaml/v3"
)

func readCircleCI(root, path string) (ciWorkflow, error) {
	w := ciWorkflow{Path: path, Provider: "circleci", Jobs: map[string]runtimeJob{}}
	document, err := readCIYAML(root, path)
	if err != nil {
		return w, err
	}
	fields := ciMembers(document)
	nodeOrb := ciScalar(ciMembers(fields["orbs"])["node"])
	verifiedNodeOrb := strings.HasPrefix(nodeOrb, "circleci/node@") && exactRelease(strings.TrimPrefix(nodeOrb, "circleci/node@"))
	jobs, commands, executors := ciMembers(fields["jobs"]), ciMembers(fields["commands"]), ciMembers(fields["executors"])
	if ciScalar(fields["setup"]) == "true" {
		w.Unresolved = append(w.Unresolved, path+": dynamic continuation configuration must be supplied before its test jobs can be discovered")
	}
	type invocation struct {
		name, label string
		parameters  map[string]string
	}
	var invocations []invocation
	for _, workflowName := range slices.Sorted(maps.Keys(ciMembers(fields["workflows"]))) {
		workflow := ciMembers(ciMembers(fields["workflows"])[workflowName])
		list := workflow["jobs"]
		if list == nil {
			continue
		}
		for index, entry := range list.Content {
			name := ciScalar(entry)
			parameters := map[string]string{}
			label := ""
			if entry.Kind == yaml.MappingNode {
				for key, node := range ciMembers(entry) {
					name = key
					parameters = ciEnvironment(node)
					if ciMembers(node)["matrix"] != nil {
						w.Unresolved = append(w.Unresolved, path+" / "+workflowName+" / "+key+": CircleCI matrix requires expanded job parameters")
					}
					label = parameters["name"]
				}
			}
			if label == "" {
				label = name
			}
			invocations = append(invocations, invocation{name, workflowName + " / " + label + " [" + strconv.Itoa(index+1) + "]", parameters})
		}
	}
	if len(invocations) == 0 {
		for _, name := range slices.Sorted(maps.Keys(jobs)) {
			invocations = append(invocations, invocation{name, name, nil})
		}
	}
	for _, inv := range invocations {
		definition, ok := jobs[inv.name]
		if !ok {
			w.Unresolved = append(w.Unresolved, path+" / "+inv.label+": external orb job "+inv.name+" requires its pinned expanded configuration")
			continue
		}
		jobFields := ciMembers(definition)
		params := map[string]string{}
		for key, node := range ciMembers(jobFields["parameters"]) {
			if value := ciMembers(node)["default"]; value != nil {
				params[key] = ciScalar(value)
			}
		}
		maps.Copy(params, inv.parameters)
		replace := func(value string) string {
			for key, v := range params {
				value = strings.ReplaceAll(value, "<< parameters."+key+" >>", v)
			}
			return value
		}
		executorName := ciScalar(jobFields["executor"])
		if executorName == "" {
			executorName = ciScalar(ciMembers(jobFields["executor"])["name"])
		}
		if executorName != "" {
			base, ok := executors[executorName]
			if !ok {
				w.Unresolved = append(w.Unresolved, path+" / "+inv.label+": external executor "+executorName+" requires resolved runtime metadata")
			}
			merged := ciMembers(base)
			maps.Copy(merged, jobFields)
			jobFields = merged
		}
		ciDirectory := replace(ciScalar(jobFields["working_directory"]))
		checkoutAtRoot := false
		if steps := jobFields["steps"]; steps != nil {
			for _, step := range steps.Content {
				checkoutAtRoot = checkoutAtRoot || (step.Kind == yaml.ScalarNode && step.Value == "checkout")
			}
		}
		directory, dirErr := literalCIPath(ciDirectory)
		if checkoutAtRoot && !strings.ContainsAny(ciDirectory, "$<>{}") {
			directory, dirErr = ".", nil
		}
		mapDirectory := func(value string) (string, error) {
			if checkoutAtRoot && ciDirectory != "" {
				if value == ciDirectory {
					return ".", nil
				}
				if rest, ok := strings.CutPrefix(value, strings.TrimSuffix(ciDirectory, "/")+"/"); ok {
					return literalCIPath(rest)
				}
			}
			return literalCIPath(value)
		}
		job := runtimeJob{}
		if _, matrix := inv.parameters["matrix"]; matrix {
			job.Gaps = append(job.Gaps, "CircleCI matrix requires expanded job parameters before editing its shared source")
		}
		parameterEnvironment := func(node *yaml.Node) map[string]string {
			env := ciEnvironment(node)
			for key, value := range env {
				env[key] = replace(value)
				if strings.Contains(env[key], "<<") {
					job.Gaps = append(job.Gaps, "CircleCI environment "+key+" requires resolved parameters")
				}
			}
			return env
		}
		environment := parameterEnvironment(jobFields["environment"])
		job.Defaults.Run.WorkingDirectory = directory
		if dirErr != nil {
			w.Unresolved = append(w.Unresolved, path+" / "+inv.label+": "+dirErr.Error())
		}
		image := ""
		if docker := jobFields["docker"]; docker != nil && len(docker.Content) > 0 {
			image = replace(ciScalar(ciMembers(docker.Content[0])["image"]))
			environment = mergeEnvironment(parameterEnvironment(ciMembers(docker.Content[0])["environment"]), environment)
			if len(docker.Content) > 1 {
				w.Unresolved = append(w.Unresolved, path+" / "+inv.label+": additional CircleCI service containers require local service facts")
			}
		}
		job.Env = providerEnvironment("circleci", environment)
		job.Node, job.NodeSource = ciImageNode(image, job.Env), "CircleCI executor image "+image
		var expand func(*yaml.Node, int)
		expand = func(list *yaml.Node, depth int) {
			if list == nil {
				return
			}
			if depth > 16 {
				w.Unresolved = append(w.Unresolved, path+": reusable commands exceed 16 levels")
				return
			}
			for _, entry := range list.Content {
				if entry.Kind == yaml.ScalarNode {
					switch entry.Value {
					case "checkout", "setup_remote_docker":
						continue
					}
					if command := commands[entry.Value]; command != nil {
						expand(ciMembers(command)["steps"], depth+1)
						continue
					}
					w.Unresolved = append(w.Unresolved, path+" / "+inv.label+": unresolved reusable command "+entry.Value)
					continue
				}
				for _, key := range slices.Sorted(maps.Keys(ciMembers(entry))) {
					value := ciMembers(entry)[key]
					switch key {
					case "run":
						run := value
						properties := ciMembers(value)
						if value.Kind == yaml.MappingNode {
							run = properties["command"]
						}
						steps := nativeSteps(path, run, "circleci")
						for i := range steps {
							steps[i].Run = replace(steps[i].Run)
							steps[i].Env = parameterEnvironment(properties["environment"])
							if properties["working_directory"] != nil {
								working, e := mapDirectory(replace(ciScalar(properties["working_directory"])))
								if e != nil {
									job.Gaps = append(job.Gaps, e.Error())
								} else {
									steps[i].WorkingDirectory = working
								}
							}
							steps[i].Shell = circleCIShell(replace(ciScalar(properties["shell"])))
							if properties["background"] != nil && ciScalar(properties["background"]) != "false" {
								steps[i].ResolutionError = "CircleCI background command requires explicit local service/readiness facts"
							}
						}
						job.Steps = append(job.Steps, steps...)
					case "when", "unless":
						// Retain recognizable consumers without evaluating conditions.
						// The gap also blocks edits of their possibly shared source.
						reason := path + " / " + inv.label + ": CircleCI conditional step " + key + " requires a resolved execution path"
						job.Gaps = append(job.Gaps, reason)
						w.Unresolved = append(w.Unresolved, reason)
						expand(ciMembers(value)["steps"], depth+1)
					case "restore_cache", "save_cache", "store_artifacts", "store_test_results", "persist_to_workspace", "attach_workspace":
						w.Review = append(w.Review, path+" / "+inv.label+": "+key+" is CI storage; local prerequisites must exist or be prepared explicitly")
					case "node/install-packages":
						if !verifiedNodeOrb {
							w.Unresolved = append(w.Unresolved, path+": "+key+" is not bound to an exact official circleci/node orb")
							continue
						}
						manager := ciScalar(ciMembers(value)["pkg-manager"])
						command := map[string]string{"npm": "npm ci", "yarn": "yarn install --frozen-lockfile", "pnpm": "pnpm install --frozen-lockfile"}[manager]
						if command == "" {
							w.Unresolved = append(w.Unresolved, path+": node/install-packages requires a literal supported pkg-manager")
						} else {
							job.Steps = append(job.Steps, runtimeStep{Run: command, Source: path + " / node/install-packages"})
						}
					case "node/install-pnpm":
						if !verifiedNodeOrb {
							w.Unresolved = append(w.Unresolved, path+": "+key+" is not bound to an exact official circleci/node orb")
							continue
						}
						w.Review = append(w.Review, path+" / "+inv.label+": pnpm setup version "+ciScalar(ciMembers(value)["version"])+" must match the local package manager")
					default:
						if command := commands[key]; command != nil {
							if len(ciMembers(value)) > 0 {
								w.Unresolved = append(w.Unresolved, path+" / "+inv.label+": parameterized reusable command "+key+" needs expansion")
							} else {
								expand(ciMembers(command)["steps"], depth+1)
							}
						} else {
							w.Unresolved = append(w.Unresolved, path+" / "+inv.label+": orb/conditional step "+key+" needs its resolved configuration")
						}
					}
				}
			}
		}
		expand(jobFields["steps"], 0)
		if strings.Contains(fmt.Sprint(params), "<<") {
			w.Unresolved = append(w.Unresolved, path+" / "+inv.label+": pipeline/matrix parameters require explicit values")
		}
		if ciMembers(definition)["parallelism"] != nil {
			w.Review = append(w.Review, path+" / "+inv.label+": parallel shards require explicit local selection; remote shard execution is not validated")
		}
		w.Jobs[inv.label] = job
	}
	return w, nil
}

func circleCIShell(shell string) string {
	words, err := shellquote.Split(shell)
	if err != nil || len(words) == 0 {
		return shell
	}
	if slices.Contains([]string{"/bin/bash", "/usr/bin/bash", "/bin/sh", "/usr/bin/sh"}, words[0]) {
		words[0] = strings.TrimPrefix(strings.TrimPrefix(words[0], "/usr"), "/bin/")
	}
	// CircleCI supplies a script file implicitly; the shared representation
	// uses {0}. Keep unsupported interpreters and options as explicit gaps.
	candidate := strings.Join(append(words, "{0}"), " ")
	if supportedCIShell(candidate) {
		return candidate
	}
	return shell
}
