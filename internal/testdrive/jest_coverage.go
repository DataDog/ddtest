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
const main = process.argv[1] && fs.realpathSync(process.argv[1]);
if (main && /(?:^|\/)(?:jest|jest-cli)\/bin\/jest\.js$/.test(main.replace(/\\/g, '/'))) {
  const request = %s;
  const args = process.argv;
  const requireCLI = require('module').createRequire(main);
  const root = path.dirname(requireCLI.resolve('jest-cli/package.json'));
  const legacy = path.join(root, 'build/cli/args.js');
  const options = fs.existsSync(legacy) ? require(legacy).options : requireCLI('jest-cli').yargsOptions;
  if (!options) throw new Error('ddtest could not inspect Jest option definitions; probe selection remains unvalidated');
  const definitions = new Map();
  for (const [name, option] of Object.entries(options)) {
    for (const alias of [name, name.replace(/[A-Z]/g, c => '-' + c.toLowerCase()), ...[].concat(option.alias || [])]) {
      definitions.set(alias, {name, ...option});
    }
  }
  const overrides = {coverageDirectory: request.coverage, outputFile: request.output};
  if (request.probe) Object.assign(overrides, {coverageThreshold: '{}', testNamePattern: '^ddtest validation probe$'});
  const kept = args.slice(0, 2);
  for (let i = 2; i < args.length; i++) {
    const token = args[i];
    if (token === '--') {if (!request.probe) kept.push(...args.slice(i)); break;}
    if (!token.startsWith('-')) {if (!request.probe) kept.push(token); continue;}
    const [flag] = token.split('=');
    const definition = definitions.get(flag.replace(/^--?/, '').replace(/^no-/, ''));
    if (!definition) {
      if (request.probe) throw new Error('ddtest cannot safely classify Jest option ' + flag);
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
    if (request.probe && ['testPathPattern', 'testPathPatterns', 'runTestsByPath', 'findRelatedTests'].includes(definition.name)) continue;
    kept.push(...values);
  }
  const generated = [];
  for (const [flag, value] of Object.entries(overrides)) if (value) generated.push('--' + flag, value);
  if (request.probe) generated.push('--runTestsByPath', request.probe);
  const separator = kept.indexOf('--', 2);
  kept.splice(separator < 0 ? kept.length : separator, 0, ...generated);
  args.splice(0, args.length, ...kept);
}
`

func redirectJestArguments(directory, coverage, output, probe string, env map[string]string) error {
	request, err := json.Marshal(map[string]string{"coverage": coverage, "output": output, "probe": probe})
	if err != nil {
		return err
	}
	path := filepath.Join(directory, "jest-arguments.cjs")
	if err := os.WriteFile(path, []byte(strings.Replace(jestArguments, "%s", string(request), 1)), 0600); err != nil {
		return err
	}
	// Initialize tracing before loading Jest's own option definitions.
	env["NODE_OPTIONS"] += " --require " + strconv.Quote(path)
	return nil
}
