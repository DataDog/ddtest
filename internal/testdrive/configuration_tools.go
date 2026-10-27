package testdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
)

// These are the Node tools classified as build prerequisites by discovery.
// Package bin metadata, not a global executable or a download, selects the entry.
var preparationPackages = map[string]string{
	"tsc": "typescript", "tsgo": "@typescript/native-preview",
	"rollup": "rollup", "webpack": "webpack", "tsup": "tsup", "unbuild": "unbuild",
}

func (t *Testdrive) loaderPreparationTool(ctx context.Context, directory, tool string, args []string, env map[string]string) (string, []string, error) {
	loader, err := platform.FindPnPLoader(directory)
	if err != nil {
		return "", nil, err
	}
	if loader == "" || preparationPackages[tool] == "" {
		return "", nil, fmt.Errorf("preparation tool not installed: %s; install project dependencies first", tool)
	}
	const marker = "__DDTEST_PREPARATION_BIN__"
	const script = `const fs=require('fs'),path=require('path');
const manifest=require.resolve(process.argv[1]+'/package.json',{paths:[process.cwd()]});
const metadata=JSON.parse(fs.readFileSync(manifest,'utf8'));
const bin=typeof metadata.bin==='string'?metadata.bin:metadata.bin?.[process.argv[2]];
if(!bin)throw new Error('Installed package has no matching bin');
process.stdout.write('__DDTEST_PREPARATION_BIN__'+JSON.stringify(path.resolve(path.dirname(manifest),bin)));`
	relative, err := filepath.Rel(workspace.CanonicalRoot(t.repositoryRoot), directory)
	if err != nil {
		return "", nil, err
	}
	data, err := setupOutput(ctx, t.executor, t.repositoryRoot, relative, "node", []string{"--require", loader, "-e", script, preparationPackages[tool], tool}, env)
	if err != nil {
		return "", nil, fmt.Errorf("resolve preparation tool %s through the project loader: %w", tool, err)
	}
	index := strings.LastIndex(string(data), marker)
	var entry string
	if index < 0 || json.NewDecoder(strings.NewReader(string(data[index+len(marker):]))).Decode(&entry) != nil || !filepath.IsAbs(entry) {
		return "", nil, fmt.Errorf("preparation tool not installed or resolvable through the project loader: %s", tool)
	}
	return "node", append([]string{"--require", loader, entry}, args...), nil
}
