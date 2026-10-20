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
	"strconv"
)

// Capture from the Jest process after package-manager expansion, rather than
// assuming that node on ddtest's PATH is the runtime selected by the command.
const jestRuntimeCapture = `
const fs = require('node:fs');
const main = process.argv[1];
if (main) {
  const path = fs.realpathSync(main).replace(/\\/g, '/');
  if (/(?:^|\/)(?:jest|jest-cli)\/bin\/jest\.js$/.test(path)) {
    fs.writeFileSync(process.env.DDTEST_JEST_PREFLIGHT, JSON.stringify({
      node: process.version, executable: process.execPath, arguments: process.argv.slice(2)
    }), {mode: 0o600});
  }
}
`

type jestRuntime struct {
	Node       string   `json:"node"`
	Executable string   `json:"executable"`
	Arguments  []string `json:"arguments"`
}

func prepareJestRuntimeCapture(env map[string]string) (string, string, error) {
	directory, err := os.MkdirTemp("", "ddtest-jest-preflight-")
	if err != nil {
		return "", "", err
	}
	script := filepath.Join(directory, "runtime.cjs")
	if err := os.WriteFile(script, []byte(jestRuntimeCapture), 0600); err != nil {
		_ = os.RemoveAll(directory)
		return "", "", err
	}
	capture := filepath.Join(directory, "runtime.json")
	env["DDTEST_JEST_PREFLIGHT"] = capture
	env["NODE_OPTIONS"] = "--require " + strconv.Quote(script) + " " + env["NODE_OPTIONS"]
	return directory, capture, nil
}

func readJestRuntimeCapture(path string) (jestRuntime, error) {
	var runtime jestRuntime
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &runtime) != nil || runtime.Node == "" || runtime.Executable == "" {
		return runtime, fmt.Errorf("could not identify the Node runtime that executed Jest; use --command with the actual Node/Jest executable")
	}
	return runtime, nil
}
