// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Only consume a literal global prefix before the npm subcommand. Other npm
// options remain unresolved instead of being mistaken for forwarded Jest flags.
func npmPrefix(args []string) (string, []string, error) {
	if len(args) == 0 {
		return "", args, nil
	}
	var prefix string
	if args[0] == "--prefix" {
		if len(args) < 3 {
			return "", nil, fmt.Errorf("npm --prefix requires a directory and subcommand")
		}
		prefix, args = args[1], args[2:]
	} else if value, ok := strings.CutPrefix(args[0], "--prefix="); ok {
		prefix, args = value, args[1:]
	} else {
		return "", args, nil
	}
	if prefix == "" || len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", nil, fmt.Errorf("unsupported npm --prefix selection requires review")
	}
	return prefix, args, nil
}

func repositoryDirectory(root, directory, prefix string) (string, error) {
	if filepath.IsAbs(prefix) || strings.ContainsAny(prefix, "$`~*?[]{}") {
		return "", fmt.Errorf("npm --prefix must name a static repository directory: %s", prefix)
	}
	target, err := filepath.EvalSymlinks(filepath.Join(directory, prefix))
	if err != nil {
		return "", fmt.Errorf("cannot resolve npm --prefix directory: %w", err)
	}
	boundary, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(boundary, target)
	if err != nil || !filepath.IsLocal(relative) {
		return "", fmt.Errorf("npm --prefix directory escapes the repository: %s", prefix)
	}
	return target, nil
}

// npmScriptOptions are npm's own options on a run/test invocation. npm parses
// every option before "--" itself instead of forwarding it to the script; only
// these are known to leave the selected script unchanged.
type npmScriptOptions struct {
	directory string // -w/--workspace or --prefix, relative to the caller
	ifPresent bool
}

func npmScriptArguments(args []string) ([]string, npmScriptOptions, error) {
	var options npmScriptOptions
	var rest []string
	for i := 0; i < len(args); i++ {
		name, value, inline := strings.Cut(args[i], "=")
		switch name {
		case "--":
			return append(rest, args[i:]...), options, nil
		case "--if-present":
			if inline && value != "true" {
				return nil, options, fmt.Errorf("unsupported npm --if-present value")
			}
			options.ifPresent = true
		case "--silent", "-s", "--quiet", "-q":
			if inline {
				return nil, options, fmt.Errorf("unsupported npm %s value", name)
			}
		case "--loglevel":
			if !inline {
				i++
			}
		case "-w", "--workspace", "--prefix":
			if !inline {
				if i+1 >= len(args) {
					return nil, options, fmt.Errorf("npm %s requires a directory", name)
				}
				i++
				value = args[i]
			}
			if options.directory != "" {
				return nil, options, fmt.Errorf("multiple npm package selections require review")
			}
			options.directory = value
		default:
			rest = append(rest, args[i])
		}
	}
	return rest, options, nil
}

func packageScriptExists(directory, name string) bool {
	data, err := os.ReadFile(filepath.Join(directory, "package.json"))
	if err != nil {
		return false
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	return json.Unmarshal(data, &manifest) == nil && manifest.Scripts[name] != ""
}
