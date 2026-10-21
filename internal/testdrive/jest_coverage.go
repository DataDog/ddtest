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

// Applied equally to baseline and instrumented processes. npm can introduce
// duplicate flags after expanding nested package scripts; Go cannot remove those
// from the outer command. Keep the recorded temporary output override exactly
// once, without changing the package script or any other Jest option.
const coverageArguments = `
const target = %s;
const args = process.argv;
const matches = [];
for (let i = 2; i < args.length; i++) {
  const match = /^(--coverageDirectory|--coverage-directory)(?:=(.*))?$/.exec(args[i]);
  if (!match) continue;
  const index = i;
  const value = match[2] === undefined ? args[++i] : match[2];
  matches.push({index, count: i - index + 1, value});
}
if (matches.length > 1 && matches[matches.length - 1].value === target) {
  for (const match of matches.slice(0, -1).reverse()) {
    args.splice(match.index, match.count);
  }
}
`

func redirectJestCoverage(directory, coverage string, env map[string]string) error {
	target, err := json.Marshal(coverage)
	if err != nil {
		return err
	}
	path := filepath.Join(directory, "coverage-arguments.cjs")
	if err := os.WriteFile(path, []byte(strings.Replace(coverageArguments, "%s", string(target), 1)), 0600); err != nil {
		return err
	}
	env["NODE_OPTIONS"] = "--require " + strconv.Quote(path) + " " + env["NODE_OPTIONS"]
	return nil
}
