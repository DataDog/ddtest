// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/ext"
)

// Ruby resolves an overlay bundle inside the session. Bundler sees the project's
// original Gemfile (including relative gemspecs), but never writes its lockfile.
type Ruby struct {
	root     string
	version  string
	executor commandExecutor
}

func NewRuby(root, version string) *Ruby {
	return &Ruby{root: root, version: version, executor: &ext.DefaultCommandExecutor{}}
}

func (r *Ruby) Install(ctx context.Context, directory string) (string, error) {
	if strings.Contains(directory, " ") {
		return "", fmt.Errorf("the Ruby tracer's native extensions cannot build in paths containing spaces; run testdrive from a checkout without spaces")
	}
	selection := ""
	version := strings.NewReplacer(`\`, `\\`, "'", `\'`).Replace(r.version)
	if ref, ok := strings.CutPrefix(version, "git:"); ok {
		if ref == "" {
			return "", fmt.Errorf("tracer git ref must not be empty")
		}
		selection = ", git: 'https://github.com/DataDog/datadog-ci-rb.git', ref: '" + ref + "'"
	} else if version != "" && version != "latest" {
		selection = ", '" + version + "'"
	}
	gemfile := filepath.Join(directory, "Gemfile")
	path := strings.ReplaceAll(strings.ReplaceAll(filepath.Join(r.root, "Gemfile"), `\`, `\\`), "'", `\'`)
	contents := "source 'https://rubygems.org'\neval_gemfile '" + path + "'\n" +
		"dependencies.reject! { |dependency| dependency.name == 'datadog-ci' }\n" +
		"gem 'datadog-ci'" + selection + "\n"
	if err := os.WriteFile(gemfile, []byte(contents), 0600); err != nil {
		return "", fmt.Errorf("write isolated Gemfile: %w", err)
	}
	if err := copyRubyLockfile(r.root, directory); err != nil {
		return "", err
	}
	if err := copyRubyBundleConfig(r.root, directory); err != nil {
		return "", err
	}
	args := []string{"install"}
	lock, err := os.ReadFile(filepath.Join(directory, "Gemfile.lock"))
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read isolated lockfile: %w", err)
	}
	// Refresh a copied tracer pin while preserving other locked dependencies.
	if strings.Contains(string(lock), "\n    datadog-ci (") {
		args = []string{"update", "datadog-ci", "--conservative"}
	}
	env := RubyEnvironment(gemfile)
	if output, err := r.executor.CombinedOutput(ctx, "bundle", args, env); err != nil {
		return "", commandError("install isolated Ruby bundle", output, err)
	}
	return gemfile, nil
}

func copyRubyBundleConfig(root, directory string) error {
	contents, err := os.ReadFile(filepath.Join(root, ".bundle", "config"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read project Bundler config: %w", err)
	}
	configDirectory := filepath.Join(directory, "bundle-config")
	if err := os.MkdirAll(configDirectory, 0700); err != nil {
		return fmt.Errorf("create isolated Bundler config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "config"), contents, 0600); err != nil {
		return fmt.Errorf("copy project Bundler config: %w", err)
	}
	return nil
}

// Preserve the customer's resolved versions while adding the tracer. PATH
// sources in a lockfile are relative to its Gemfile, so relocate those sources
// when copying it into the session. The original remains untouched.
func copyRubyLockfile(root, directory string) error {
	contents, err := os.ReadFile(filepath.Join(root, "Gemfile.lock"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read project lockfile: %w", err)
	}
	lines := strings.Split(string(contents), "\n")
	inPath := false
	for i, line := range lines {
		if line != "" && !strings.HasPrefix(line, " ") {
			inPath = line == "PATH"
		}
		if inPath && strings.HasPrefix(line, "  remote: ") {
			path := strings.TrimPrefix(line, "  remote: ")
			if !filepath.IsAbs(path) {
				lines[i] = "  remote: " + filepath.Join(root, path)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(directory, "Gemfile.lock"), []byte(strings.Join(lines, "\n")), 0600); err != nil {
		return fmt.Errorf("copy project lockfile: %w", err)
	}
	return nil
}

func RubyEnvironment(gemfile string) map[string]string {
	return map[string]string{
		"BUNDLE_GEMFILE":    gemfile,
		"BUNDLE_PATH":       filepath.Join(filepath.Dir(gemfile), "gems"),
		"BUNDLE_APP_CONFIG": filepath.Join(filepath.Dir(gemfile), "bundle-config"),
		"BUNDLE_FROZEN":     "false", "BUNDLE_DEPLOYMENT": "false",
		"BUNDLE_WITH": "", "BUNDLE_WITHOUT": "", "BUNDLE_ONLY": "",
	}
}
