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
	"github.com/DataDog/ddtest/internal/platform"
)

// Ruby reuses the project tracer, adding an isolated overlay bundle only when absent.
type Ruby struct {
	root     string
	version  string
	executor commandExecutor
}

func NewRuby(root, version string) *Ruby {
	return &Ruby{root: root, version: version, executor: &ext.DefaultCommandExecutor{}}
}

func (r *Ruby) Install(ctx context.Context, directory string) (Installation, error) {
	selection := ""
	version := strings.NewReplacer(`\`, `\\`, "'", `\'`).Replace(r.version)
	if ref, ok := strings.CutPrefix(version, "git:"); ok {
		if ref == "" {
			return Installation{}, fmt.Errorf("tracer git ref must not be empty")
		}
		selection = ", git: 'https://github.com/DataDog/datadog-ci-rb.git', ref: '" + ref + "'"
	} else if version != "" && version != "latest" {
		selection = ", '" + version + "'"
	}
	projectGemfile := os.Getenv("BUNDLE_GEMFILE")
	if projectGemfile == "" {
		projectGemfile = filepath.Join(r.root, "Gemfile")
	}
	project, err := platform.DetectRubyTracer(ctx, r.executor, map[string]string{"BUNDLE_GEMFILE": projectGemfile, "RUBYOPT": ""})
	if err != nil {
		return Installation{}, err
	}
	if project != "" {
		return Installation{Project: true}, nil
	}
	if strings.Contains(directory, " ") {
		return Installation{}, fmt.Errorf("the Ruby tracer's native extensions cannot build in paths containing spaces; run testdrive from a checkout without spaces")
	}
	gemfile := filepath.Join(directory, "Gemfile")
	path := strings.ReplaceAll(strings.ReplaceAll(filepath.Join(r.root, "Gemfile"), `\`, `\\`), "'", `\'`)
	contents := "source 'https://rubygems.org'\neval_gemfile '" + path + "'\n" +
		"gem 'datadog-ci'" + selection + "\n"
	if err := os.WriteFile(gemfile, []byte(contents), 0600); err != nil {
		return Installation{}, fmt.Errorf("write isolated Gemfile: %w", err)
	}
	if err := copyRubyLockfile(r.root, directory); err != nil {
		return Installation{}, err
	}
	if err := copyRubyBundleConfig(r.root, directory); err != nil {
		return Installation{}, err
	}
	env := RubyEnvironment(gemfile)
	if output, err := r.executor.CombinedOutput(ctx, "bundle", []string{"install"}, env); err != nil {
		return Installation{}, commandError("install isolated Ruby bundle", output, err)
	}
	return Installation{Path: gemfile}, nil
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
	if gemfile == "" {
		return nil
	}
	return map[string]string{
		"BUNDLE_GEMFILE":    gemfile,
		"BUNDLE_PATH":       filepath.Join(filepath.Dir(gemfile), "gems"),
		"BUNDLE_APP_CONFIG": filepath.Join(filepath.Dir(gemfile), "bundle-config"),
		"BUNDLE_FROZEN":     "false", "BUNDLE_DEPLOYMENT": "false",
		"BUNDLE_WITH": "", "BUNDLE_WITHOUT": "", "BUNDLE_ONLY": "",
	}
}
