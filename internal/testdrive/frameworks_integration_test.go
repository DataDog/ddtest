// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestPublicFrameworkTestdrives exercises the shipped CLI, real tracers, and
// real test runners. Browser downloads are explicit test setup, never testdrive
// side effects. Run with DDTEST_RUN_FRAMEWORK_INTEGRATION_TEST=1.
func TestPublicFrameworkTestdrives(t *testing.T) {
	if os.Getenv("DDTEST_RUN_FRAMEWORK_INTEGRATION_TEST") == "" {
		t.Skip("set DDTEST_RUN_FRAMEWORK_INTEGRATION_TEST=1")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "ddtest")
	integrationCommand(t, ctx, "../..", nil, "go", "build", "-o", binary, ".")
	fixtures := []struct {
		name, manifest, command string
		files                   map[string]string
	}{
		{"jest", `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.5.1"}}`, "npm test", map[string]string{"one.test.js": `test('adds', () => expect(1+1).toBe(2));`}},
		{"mocha", `{"scripts":{"test":"mocha"},"devDependencies":{"mocha":"11.7.5"}}`, "npm test", map[string]string{"test/one.js": `const assert = require('node:assert'); it('adds', () => assert.equal(1+1,2));`}},
		{"vitest", `{"type":"module","scripts":{"test":"vitest run"},"devDependencies":{"vitest":"3.2.4"}}`, "npm test", map[string]string{"one.test.js": `import {test,expect} from 'vitest'; test('adds', () => expect(1+1).toBe(2));`}},
		{"playwright", `{"scripts":{"test":"playwright test"},"devDependencies":{"@playwright/test":"1.55.1"}}`, "npm test", map[string]string{"one.spec.js": `const {test,expect} = require('@playwright/test'); test('adds', () => expect(1+1).toBe(2));`}},
		{"cucumber", `{"scripts":{"test":"cucumber-js"},"devDependencies":{"@cucumber/cucumber":"12.2.0"}}`, "npm test", map[string]string{"features/one.feature": "Feature: Arithmetic\n  Background:\n    Given addition works\n  Scenario: Add\n    Given addition works\n", "features/step_definitions/one.js": `const {Given} = require('@cucumber/cucumber'); Given('addition works', () => require('node:assert').equal(1+1,2));`}},
		{"cypress", `{"scripts":{"test":"cypress run"},"devDependencies":{"cypress":"15.1.0"}}`, "npm test", map[string]string{"cypress.config.js": `module.exports={e2e:{supportFile:false,setupNodeEvents(on,config){on('task',{answer:()=>42});on('after:run',()=>{require('node:fs').writeFileSync('original-hook.txt','ran');});return config;}}};`, "cypress/e2e/one.cy.js": `it('preserves hooks', () => { cy.task('answer').should('equal',42); });`}},
		{"pytest", "", "python -m pytest", map[string]string{"requirements.txt": "pytest==8.4.2\n", "pytest.ini": "[pytest]\n", "test_one.py": "def test_adds():\n    assert 1 + 1 == 2\n"}},
		{"rspec", "", "bundle exec rspec", map[string]string{"Gemfile": "source 'https://rubygems.org'\ngem 'rspec', '3.13.1'\n", "spec/one_spec.rb": "RSpec.describe 'addition' do\n  it('adds') { expect(1 + 1).to eq(2) }\nend\n"}},
		{"minitest", "", "bundle exec rake test", map[string]string{"Gemfile": "source 'https://rubygems.org'\ngem 'rake', '13.3.0'\ngem 'minitest', '5.25.5'\n", "Rakefile": "require 'rake/testtask'\nRake::TestTask.new(:test) { |t| t.pattern = 'test/**/*_test.rb' }\n", "test/one_test.rb": "require 'minitest/autorun'\nclass AdditionTest < Minitest::Test\n  def test_adds\n    assert_equal 2, 1 + 1\n  end\nend\n"}},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			name := "project space"
			if fixture.name == "rspec" || fixture.name == "minitest" {
				name = "project"
			} // Keep the recorded Ruby fixture path; build failures retain Bundler diagnostics.
			root := filepath.Join(t.TempDir(), name)
			require.NoError(t, os.MkdirAll(root, 0755))
			integrationCommand(t, ctx, root, nil, "git", "init", "-q")
			for name, contents := range fixture.files {
				integrationFile(t, root, name, contents)
			}
			if fixture.manifest != "" {
				integrationFile(t, root, "package.json", fixture.manifest)
			}
			integrationFile(t, root, ".github/workflows/test.yml", "name: tests\non: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: "+fixture.command+"\n")
			env := []string{}
			if fixture.manifest != "" {
				integrationCommand(t, ctx, root, env, "npm", "install", "--no-audit", "--no-fund")
			}
			if fixture.name == "pytest" {
				venv := filepath.Join(t.TempDir(), "venv")
				integrationCommand(t, ctx, root, nil, "python3", "-m", "venv", venv)
				env = append(env, "PATH="+filepath.Join(venv, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
				integrationCommand(t, ctx, root, env, filepath.Join(venv, "bin", "python"), "-m", "pip", "install", "pytest==8.4.2")
			}
			if fixture.name == "rspec" || fixture.name == "minitest" {
				integrationCommand(t, ctx, root, env, "bundle", "lock")
			}
			before := map[string]string{}
			for _, name := range []string{"package.json", "package-lock.json", "Gemfile", "Gemfile.lock", "requirements.txt", "cypress.config.js"} {
				contents, err := os.ReadFile(filepath.Join(root, name))
				if err == nil {
					before[name] = string(contents)
				}
			}
			output := integrationCommand(t, ctx, root, env, binary, "testdrive", "--yes")
			require.Contains(t, output, "Test events received.")
			require.Contains(t, output, "Open report:")
			reports, err := filepath.Glob(filepath.Join(root, ".testoptimization", "testdrive", "*", "report.html"))
			require.NoError(t, err)
			require.Len(t, reports, 1)
			contents, err := os.ReadFile(reports[0])
			require.NoError(t, err)
			require.Contains(t, string(contents), "Test events received.")
			traffic, err := filepath.Glob(filepath.Join(filepath.Dir(reports[0]), "intake", "*citestcycle.json"))
			require.NoError(t, err)
			require.NotEmpty(t, traffic)
			for name, contents := range before {
				if (fixture.name == "rspec" || fixture.name == "minitest") && (name == "Gemfile" || name == "Gemfile.lock") {
					continue // bundle add updates Ruby dependency files.
				}
				after, err := os.ReadFile(filepath.Join(root, name))
				require.NoError(t, err)
				require.Equal(t, contents, string(after), name)
			}
			if fixture.name == "rspec" || fixture.name == "minitest" {
				gemfile, err := os.ReadFile(filepath.Join(root, "Gemfile"))
				require.NoError(t, err)
				require.Contains(t, string(gemfile), "datadog-ci")
				require.FileExists(t, filepath.Join(root, "Gemfile.lock"))
			} else if _, existed := before["Gemfile.lock"]; !existed {
				_, err := os.Stat(filepath.Join(root, "Gemfile.lock"))
				require.True(t, os.IsNotExist(err), "project lockfile must not be created")
			}
			if fixture.name == "cypress" {
				hook, err := os.ReadFile(filepath.Join(root, "original-hook.txt"))
				require.NoError(t, err)
				require.Equal(t, "ran", string(hook))
			}
			t.Log(strings.TrimSpace(output))
		})
	}
}

func integrationFile(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0644))
}

func integrationCommand(t *testing.T, ctx context.Context, directory string, env []string, name string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = directory
	command.Env = append(os.Environ(), env...)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s %v:\n%s", name, args, output)
	return string(output)
}
