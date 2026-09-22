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
	"strings"
)

// prepareCypress wraps configuration inside the session; project config and
// support files are neither edited nor replaced on disk.
func prepareCypress(root, directory, preload string, args []string) ([]string, error) {
	var config string
	clean := make([]string, 0, len(args)+2)
	for i := 0; i < len(args); i++ {
		if args[i] == "--config-file" {
			if i+1 == len(args) {
				return nil, fmt.Errorf("--config-file requires a path")
			}
			i++
			config = args[i]
			continue
		}
		if strings.HasPrefix(args[i], "--config-file=") {
			config = strings.TrimPrefix(args[i], "--config-file=")
			continue
		}
		clean = append(clean, args[i])
	}
	if config == "" {
		for _, name := range []string{"cypress.config.ts", "cypress.config.js", "cypress.config.mjs", "cypress.config.cjs"} {
			if _, err := os.Stat(filepath.Join(root, name)); err == nil {
				config = name
				break
			}
		}
	}
	if config == "" {
		return nil, fmt.Errorf("cypress needs a config file; provide its path with --command '... --config-file path'")
	}
	if !filepath.IsAbs(config) {
		config = filepath.Join(root, config)
	}
	values, _ := json.Marshal(map[string]string{"root": root, "config": config, "directory": directory, "tracer": filepath.Dir(filepath.Dir(preload))})
	wrapper := "const options = " + string(values) + ";\n" + cypressWrapper
	path := filepath.Join(directory, "cypress.config.cjs")
	if err := os.WriteFile(path, []byte(wrapper), 0600); err != nil {
		return nil, err
	}
	return append(clean, "--config-file", path), nil
}

const cypressWrapper = `
const fs = require('node:fs');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
// Cypress starts the config process in the wrapper's directory. Keep relative
// filesystem operations in the customer's config and hooks rooted in the project.
process.chdir(options.root);
module.exports = (async () => {
  let original;
  try { original = require(options.config); }
  catch (error) {
    if (error.code !== 'ERR_REQUIRE_ESM') throw error;
    original = await import(pathToFileURL(options.config).href);
  }
  original = original.default || original;
  const config = { ...original };
  for (const type of ['e2e', 'component']) {
    if (!original[type]) continue;
    const setup = original[type].setupNodeEvents;
    config[type] = { ...original[type], async setupNodeEvents(on, resolved) {
      // Compose hooks so adding instrumentation cannot discard customer hooks.
      const handlers = new Map();
      const collect = (event, handler) => {
        if (event === 'task') { handlers.set(event, { ...(handlers.get(event) || {}), ...handler }); return; }
        const previous = handlers.get(event);
        handlers.set(event, previous ? async (...args) => {
          const first = await previous(...args);
          const second = await handler(...args);
          return second === undefined ? first : second;
        } : handler);
      };
      if (setup) resolved = (await setup(collect, resolved)) || resolved;
      const support = path.join(options.directory, type + '-support.cjs');
      let contents = 'require(' + JSON.stringify(path.join(options.tracer, 'ci/cypress/support')) + ');\n';
      if (resolved.supportFile) contents += 'require(' + JSON.stringify(path.resolve(resolved.projectRoot, resolved.supportFile)) + ');\n';
      fs.writeFileSync(support, contents);
      resolved.supportFile = support;
      const instrument = require(path.join(options.tracer, 'ci/cypress/plugin'));
      resolved = (await instrument(collect, resolved)) || resolved;
      for (const [event, handler] of handlers) on(event, handler);
      return resolved;
    }};
  }
  return config;
})();
`
