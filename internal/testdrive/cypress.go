// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kballard/go-shellquote"
)

// prepareCypress wraps configuration inside the session; project config and
// support files are neither edited nor replaced on disk.
func prepareCypress(root, directory, preload, command string, args []string) ([]string, error) {
	inspectionArgs := cypressInspectionArgs(root, command, args)
	projectRoot := root
	if project := optionValue(inspectionArgs, "--project", "-P"); project != "" {
		if filepath.IsAbs(project) {
			projectRoot = project
		} else {
			projectRoot = filepath.Join(root, project)
		}
	}
	var config string
	clean := make([]string, 0, len(args)+2)
	for i := 0; i < len(args); i++ {
		if args[i] == "--config-file" || args[i] == "-C" {
			if i+1 == len(args) {
				return nil, fmt.Errorf("--config-file requires a path")
			}
			i++
			config = args[i]
			continue
		}
		if strings.HasPrefix(args[i], "--config-file=") || strings.HasPrefix(args[i], "-C=") {
			_, config, _ = strings.Cut(args[i], "=")
			continue
		}
		if value, found := strings.CutPrefix(args[i], "-C"); found {
			config = value
			continue
		}
		clean = append(clean, args[i])
	}
	if config == "" {
		config = optionValue(inspectionArgs, "--config-file", "-C")
	}
	if config == "false" {
		config = ""
	} else if config == "" {
		for _, name := range []string{"cypress.config.js", "cypress.config.ts", "cypress.config.mjs", "cypress.config.cjs"} {
			if _, err := os.Stat(filepath.Join(projectRoot, name)); err == nil {
				config = name
				break
			}
		}
	}
	if config != "" && !filepath.IsAbs(config) {
		config = filepath.Join(projectRoot, config)
	}
	testingType := "e2e"
	if optionPresent(inspectionArgs, "--component") {
		testingType = "component"
	}
	tracerRoot := filepath.Dir(filepath.Dir(preload))
	values, _ := json.Marshal(map[string]string{"root": projectRoot, "directory": directory, "tracer": tracerRoot, "testingType": testingType})
	// Static imports run before the wrapper body. Restore the project directory
	// in a preceding import so config initialization also sees the correct cwd.
	cwdPath := filepath.Join(directory, "cypress-cwd.cjs")
	encodedRoot, _ := json.Marshal(projectRoot)
	if err := os.WriteFile(cwdPath, []byte("process.chdir("+string(encodedRoot)+");\n"), 0600); err != nil {
		return nil, err
	}
	// Native ESM needs URL escaping; older Cypress TypeScript loaders compile
	// imports to require(), which only accepts filesystem paths.
	esm := cypressUsesESM(directory)
	encodeImport := func(path string) []byte {
		if esm {
			path = absoluteFileURL(path)
		}
		encoded, _ := json.Marshal(path)
		return encoded
	}
	encodedCwd := encodeImport(cwdPath)
	configImport := "const originalImport = {};\n"
	if config != "" {
		encodedConfig := encodeImport(config)
		configImport = "import originalImport from " + string(encodedConfig) + ";\n"
	}
	encodedPlugin := encodeImport(filepath.Join(tracerRoot, "ci", "cypress", "plugin.js"))
	wrapper := "import fs from 'node:fs';\nimport path from 'node:path';\nimport " + string(encodedCwd) + ";\n" + configImport +
		"import instrumentImport from " + string(encodedPlugin) + ";\nconst options = " + string(values) + ";\n" + cypressWrapper
	path := filepath.Join(directory, "cypress.config.ts")
	if err := os.WriteFile(path, []byte(wrapper), 0600); err != nil {
		return nil, err
	}
	base := strings.ToLower(filepath.Base(command))
	base = strings.TrimSuffix(strings.TrimSuffix(base, ".cmd"), ".ps1")
	if base == "npm" && !slices.Contains(clean, "--") {
		clean = append(clean, "--")
	}
	return append(clean, "--config-file", path), nil
}

func cypressUsesESM(directory string) bool {
	for {
		contents, err := os.ReadFile(filepath.Join(directory, "package.json"))
		if !os.IsNotExist(err) {
			var manifest struct {
				Type string `json:"type"`
			}
			return err == nil && json.Unmarshal(contents, &manifest) == nil && manifest.Type == "module"
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return false
		}
		directory = parent
	}
}

func optionValue(args []string, options ...string) string {
	value := ""
	for index := 0; index < len(args); index++ {
		for _, option := range options {
			if args[index] == option && index+1 < len(args) {
				value = args[index+1]
				index++
				break
			}
			if candidate, found := strings.CutPrefix(args[index], option+"="); found {
				value = candidate
				break
			}
			if len(option) == 2 && strings.HasPrefix(option, "-") {
				if candidate, found := strings.CutPrefix(args[index], option); found && candidate != "" {
					value = candidate
					break
				}
			}
		}
	}
	return value
}

func optionPresent(args []string, option string) bool {
	for _, arg := range args {
		if arg == option || strings.HasPrefix(arg, option+"=") {
			return true
		}
	}
	return false
}

func cypressInspectionArgs(root, command string, args []string) []string {
	base := strings.ToLower(filepath.Base(command))
	base = strings.TrimSuffix(base, ".cmd")
	base = strings.TrimSuffix(base, ".ps1")
	if base != "npm" && base != "yarn" && base != "pnpm" && base != "bun" {
		return args
	}
	script := ""
	forwarded := 1
	if len(args) > 0 && args[0] == "test" {
		script = "test"
	} else if len(args) > 1 && (args[0] == "run" || args[0] == "run-script") {
		script = args[1]
		forwarded = 2
	} else if len(args) > 0 && base != "npm" {
		script = args[0]
	}
	if script == "" {
		return args
	}
	contents, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return args
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(contents, &manifest) != nil {
		return args
	}
	expanded, err := shellquote.Split(manifest.Scripts[script])
	if err != nil {
		return args
	}
	if manifest.Scripts[script] == "" {
		return args
	}
	if forwarded < len(args) && args[forwarded] == "--" {
		forwarded++
	}
	return append(expanded, args[forwarded:]...)
}

const cypressWrapper = `
export default (() => {
  const original = originalImport.default || originalImport;
  const config = { ...original };
  const types = new Set(['e2e', 'component'].filter(type => original[type]));
  types.add(options.testingType);
  for (const type of types) {
    const originalType = original[type] || {};
    const setup = originalType.setupNodeEvents;
    config[type] = { ...originalType, async setupNodeEvents(on, resolved) {
      // Cypress replaces project hooks and merges tasks. Compose instrumentation
      // with only the final project hook for each event.
      const handlers = new Map();
      let instrumenting = false;
      const collect = (event, handler) => {
        if (event === 'task') { handlers.set(event, { ...(handlers.get(event) || {}), ...handler }); return; }
        const previous = instrumenting ? handlers.get(event) : undefined;
        handlers.set(event, previous ? async (...args) => {
          const first = await previous(...args);
          const second = await handler(...args);
          return second === undefined ? first : second;
        } : handler);
      };
      if (setup) resolved = { ...resolved, ...((await setup(collect, resolved)) || {}) };
      const support = path.join(options.directory, type + '-support.cjs');
      let contents = 'require(' + JSON.stringify(path.join(options.tracer, 'ci/cypress/support')) + ');\n';
      if (resolved.supportFile) contents += 'require(' + JSON.stringify(path.resolve(resolved.projectRoot || options.root, resolved.supportFile)) + ');\n';
      fs.writeFileSync(support, contents);
      resolved.supportFile = support;
      const instrument = instrumentImport.default || instrumentImport;
      instrumenting = true;
      resolved = (await instrument(collect, resolved)) || resolved;
      for (const [event, handler] of handlers) on(event, handler);
      return resolved;
    }};
  }
  return config;
})();
`
