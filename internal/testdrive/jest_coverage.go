// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Normalize in the actual Jest process, after package scripts expand. Use the
// installed runner's option schema so positional filters cannot swallow option
// values. Both comparison sides receive identical output redirects; only probes
// replace test selection and whole-project coverage thresholds.
const jestArguments = `
const fs = require('fs');
const path = require('path');
let main = process.argv[1];
// With -e/-p, argv[1] is an argument, not a script; a missing script must keep
// Node's own error. Neither is Jest, so leave both untouched.
if (main && !/(?:^|\/)(?:jest|jest-cli)\/bin\/jest\.js$/.test(main.replace(/\\/g, '/'))) {
  try { main = fs.realpathSync(main); } catch { main = undefined; }
}
if (main && /(?:^|\/)(?:jest|jest-cli)\/bin\/jest\.js$/.test(main.replace(/\\/g, '/'))) {
  const request = %s;
  const args = process.argv;
  const probeSelection = Boolean(request.probe || request.probeDiscovery);
  const requireCLI = require('module').createRequire(main);
  const root = path.dirname(requireCLI.resolve('jest-cli/package.json'));
  // Jest 27/28, early 29, and later releases expose their schema differently.
  let options;
  for (const relative of ['build/cli/args.js', 'build/args.js']) {
    const file = path.join(root, relative);
    if (fs.existsSync(file)) { options = require(file).options; if (options) break; }
  }
  if (!options) options = requireCLI('jest-cli').yargsOptions;
  if (!options) throw new Error('ddtest could not inspect Jest option definitions; probe selection remains unvalidated');
  const definitions = new Map();
  for (const [name, option] of Object.entries(options)) {
    for (const alias of [name, name.replace(/[A-Z]/g, c => '-' + c.toLowerCase()), ...[].concat(option.alias || [])]) {
      definitions.set(alias, {name, ...option});
    }
  }
  const overrides = {coverageDirectory: request.coverage, outputFile: request.output, watchman: "false"};
  if (request.project) overrides.selectProjects = request.project;
  if (request.probe) Object.assign(overrides, {coverageThreshold: '{}', testNamePattern: '^ddtest validation probe$'});
  const kept = args.slice(0, 2);
  for (let i = 2; i < args.length; i++) {
    const token = args[i];
    if (token === '--') {if (!probeSelection) kept.push(...args.slice(i)); break;}
    if (!token.startsWith('-')) {if (!probeSelection) kept.push(token); continue;}
    const [flag] = token.split('=');
    const definition = definitions.get(flag.replace(/^--?/, '').replace(/^no-/, ''));
    if (!definition) {
      if (probeSelection) throw new Error('ddtest cannot safely classify Jest option ' + flag);
      kept.push(token); continue;
    }
    const values = [token];
    if (!token.includes('=') && !flag.startsWith('--no-')) {
      if (definition.type === 'array') {
        while (i+1 < args.length && !args[i+1].startsWith('-')) values.push(args[++i]);
      } else if (definition.type !== 'boolean' || definition.requiresArg) {
        if (i+1 < args.length) values.push(args[++i]);
      } else if (i+1 < args.length && /^(true|false)$/.test(args[i+1])) values.push(args[++i]);
    }
    if (overrides[definition.name]) continue;
    if (probeSelection && ['testPathPattern', 'testPathPatterns', 'runTestsByPath', 'findRelatedTests', 'shard', 'testNamePattern'].includes(definition.name)) continue;
    if (request.project && ['selectProjects','ignoreProjects'].includes(definition.name)) continue;
    kept.push(...values);
  }
  const generated = [];
  for (const [flag, value] of Object.entries(overrides)) if (value) generated.push('--' + flag, value);
  if (request.probe) generated.push('--runTestsByPath', request.probe);
  const separator = kept.indexOf('--', 2);
  kept.splice(separator < 0 ? kept.length : separator, 0, ...generated);
  args.splice(0, args.length, ...kept);
  fs.writeFileSync(request.applied, JSON.stringify({coverageThresholdsDisabled: Boolean(request.probe)}));
}
`

type jestArgumentRequest struct {
	Coverage       string `json:"coverage"`
	Output         string `json:"output"`
	Probe          string `json:"probe"`
	Project        string `json:"project"`
	ProbeDiscovery bool   `json:"probeDiscovery"`
	Applied        string `json:"applied"`
}

func redirectJestArguments(directory string, request jestArgumentRequest, env map[string]string) error {
	request.Applied = filepath.Join(directory, "jest-overrides.json")
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	path := filepath.Join(directory, "jest-arguments.cjs")
	if err := os.WriteFile(path, []byte(strings.Replace(jestArguments, "%s", string(data), 1)), 0600); err != nil {
		return err
	}
	env["NODE_OPTIONS"] += " --require " + strconv.Quote(path)
	return nil
}

func probeThresholdOverrideApplied(directory string) bool {
	var applied struct {
		Disabled bool `json:"coverageThresholdsDisabled"`
	}
	data, err := os.ReadFile(filepath.Join(directory, "jest-overrides.json"))
	return err == nil && json.Unmarshal(data, &applied) == nil && applied.Disabled
}
