// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

func readGitLab(root, path string) (ciWorkflow, error) {
	w := ciWorkflow{Path: path, Provider: "gitlab", Jobs: map[string]runtimeJob{}}
	definitions := map[string]*yaml.Node{}
	sources := map[*yaml.Node]string{}
	seen := map[string]bool{}
	var load func(string, int) error
	load = func(file string, depth int) error {
		if depth > 16 || len(seen) >= 64 {
			return fmt.Errorf("GitLab includes exceed 16 levels or 64 files")
		}
		if seen[file] {
			return fmt.Errorf("GitLab include cycle or repeated include needs review: %s", file)
		}
		seen[file] = true
		document, err := readCIYAML(root, file)
		if err != nil {
			return err
		}
		members := ciMembers(document)
		includes := members["include"]
		if includes != nil {
			entries := []*yaml.Node{includes}
			if includes.Kind == yaml.SequenceNode {
				entries = includes.Content
			}
			for _, entry := range entries {
				local := ciScalar(entry)
				if entry.Kind == yaml.MappingNode {
					local = ciScalar(ciMembers(entry)["local"])
				}
				if local == "" || strings.ContainsAny(local, "$*?[]") || strings.Contains(local, "://") || !filepath.IsLocal(strings.TrimPrefix(local, "/")) || ciMembers(entry)["rules"] != nil {
					w.Unresolved = append(w.Unresolved, file+": GitLab include requires resolved external/conditional configuration; supply its pinned contents explicitly")
					continue
				}
				if err := load(strings.TrimPrefix(local, "/"), depth+1); err != nil {
					return err
				}
			}
		}
		for key, node := range members {
			if previous := definitions[key]; previous != nil && key != "include" {
				w.Unresolved = append(w.Unresolved, file+": merged include overrides "+key+"; cross-file deep merge requires review")
			}
			definitions[key] = node
			var record func(*yaml.Node)
			record = func(n *yaml.Node) {
				sources[n] = file
				for _, child := range n.Content {
					record(child)
				}
			}
			record(node)
		}
		return nil
	}
	if err := load(path, 0); err != nil {
		return w, err
	}
	defaults := ciMembers(definitions["default"])
	for _, key := range []string{"image", "before_script"} {
		if defaults[key] == nil {
			defaults[key] = definitions[key]
		}
	}
	globalEnv := ciEnvironment(definitions["variables"])
	var resolve func(string, map[string]bool) (map[string]*yaml.Node, error)
	resolve = func(name string, visiting map[string]bool) (map[string]*yaml.Node, error) {
		if visiting[name] || len(visiting) > 16 {
			return nil, fmt.Errorf("extends cycle/depth at %s", name)
		}
		definition, ok := definitions[name]
		if !ok {
			return nil, fmt.Errorf("extends %q is not available locally", name)
		}
		visiting = maps.Clone(visiting)
		visiting[name] = true
		own := ciMembers(definition)
		merged := map[string]*yaml.Node{}
		var variables map[string]*yaml.Node
		for _, parent := range ciStrings(own["extends"]) {
			base, err := resolve(parent, visiting)
			if err != nil {
				return nil, err
			}
			maps.Copy(merged, base)
			if variables == nil {
				variables = map[string]*yaml.Node{}
			}
			maps.Copy(variables, ciMembers(base["variables"]))
		}
		maps.Copy(merged, own)
		if len(variables) > 0 {
			maps.Copy(variables, ciMembers(own["variables"]))
			combined := &yaml.Node{Kind: yaml.MappingNode}
			for _, key := range slices.Sorted(maps.Keys(variables)) {
				combined.Content = append(combined.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, variables[key])
			}
			merged["variables"] = combined
		}
		return merged, nil
	}
	reserved := []string{"include", "default", "variables", "stages", "workflow", "image", "services", "cache", "before_script", "after_script", "spec"}
	for _, name := range slices.Sorted(maps.Keys(definitions)) {
		if strings.HasPrefix(name, ".") || slices.Contains(reserved, name) {
			continue
		}
		fields, err := resolve(name, map[string]bool{})
		if err != nil {
			w.Unresolved = append(w.Unresolved, path+" / "+name+": "+err.Error())
			continue
		}
		if fields["script"] == nil {
			if fields["trigger"] != nil {
				w.Unresolved = append(w.Unresolved, path+" / "+name+": child/trigger pipeline needs its resolved test configuration")
			}
			continue
		}
		inherit := ciMembers(fields["inherit"])
		defaultPolicy, variablePolicy := inherit["default"], inherit["variables"]
		for key, node := range defaults {
			allowed := ciScalar(defaultPolicy) != "false"
			if defaultPolicy != nil && defaultPolicy.Kind == yaml.SequenceNode {
				allowed = slices.Contains(ciStrings(defaultPolicy), key)
			}
			if allowed && fields[key] == nil {
				fields[key] = node
			}
		}
		inheritedEnv := maps.Clone(globalEnv)
		if ciScalar(variablePolicy) == "false" {
			inheritedEnv = nil
		}
		if variablePolicy != nil && variablePolicy.Kind == yaml.SequenceNode {
			for key := range inheritedEnv {
				if !slices.Contains(ciStrings(variablePolicy), key) {
					delete(inheritedEnv, key)
				}
			}
		}
		env := mergeEnvironment(inheritedEnv, ciEnvironment(fields["variables"]))
		image := ciScalar(fields["image"])
		if image == "" {
			image = ciScalar(ciMembers(fields["image"])["name"])
		}
		job := runtimeJob{Env: providerEnvironment("gitlab", env), Node: ciImageNode(image, env), NodeSource: "GitLab image " + image}
		if rules := fields["rules"]; rules != nil {
			for _, rule := range rules.Content {
				if ciMembers(rule)["variables"] != nil {
					job.Gaps = append(job.Gaps, "GitLab rules change environment values; provide the resolved per-rule configuration")
				}
			}
		}
		if fields["rules"] != nil || fields["only"] != nil || fields["except"] != nil {
			w.Review = append(w.Review, path+" / "+name+": GitLab rules/filters are possible CI paths; event and branch eligibility is not validated locally")
			if rules := fields["rules"]; rules != nil && len(rules.Content) == 1 {
				r := ciMembers(rules.Content[0])
				if r["if"] == nil && r["changes"] == nil && r["exists"] == nil && ciScalar(r["when"]) == "never" {
					job.If = "false"
				}
			}
		}
		for _, key := range []string{"before_script", "script"} {
			node := fields[key]
			file := sources[node]
			if file == "" {
				file = path
			}
			job.Steps = append(job.Steps, nativeSteps(file, node, "gitlab")...)
		}
		if fields["services"] != nil {
			w.Unresolved = append(w.Unresolved, path+" / "+name+": GitLab service containers require explicit local service facts")
		}
		matrix := ciMembers(fields["parallel"])["matrix"]
		if matrix != nil {
			var rows []map[string]any
			for _, entry := range matrix.Content {
				values := map[string]any{}
				for axis, node := range ciMembers(entry) {
					items := ciStrings(node)
					list := make([]any, len(items))
					for i, v := range items {
						list[i] = v
					}
					values[axis] = list
				}
				expanded, e := runtimeMatrix(values)
				if e != nil {
					return w, e
				}
				rows = append(rows, expanded...)
				if len(rows) > 256 {
					return w, fmt.Errorf("GitLab matrix exceeds 256 entries")
				}
			}
			for i, row := range rows {
				variant := job
				variant.Env = maps.Clone(job.Env)
				for key, value := range row {
					variant.Env[key] = fmt.Sprint(value)
				}
				variant.Node = ciImageNode(image, variant.Env)
				w.Jobs[name+" ["+strconv.Itoa(i+1)+"]"] = nativeShellState(root, variant)
			}
		} else {
			w.Jobs[name] = nativeShellState(root, job)
		}
	}
	return w, nil
}
