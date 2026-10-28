// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestOnboardCommandRunsDetection(t *testing.T) {
	called := false
	command := newOnboardCommand(func(output io.Writer) error {
		called = true
		_, _ = io.WriteString(output, "onboarding instructions\n")
		return nil
	})
	if command.Short != "Start here: onboard this repository to Test Optimization" {
		t.Fatalf("Short = %q", command.Short)
	}
	var output bytes.Buffer
	command.SetOut(&output)

	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() unexpected error: %v", err)
	}
	if !called {
		t.Fatal("onboard command did not run detection")
	}
	if output.String() != "onboarding instructions\n" {
		t.Fatalf("output = %q", output.String())
	}
}

func TestOnboardApplyRequiresExplicitConsent(t *testing.T) {
	for _, args := range [][]string{{"--yes"}, {"--allow-dirty"}} {
		command := newOnboardCommand(func(io.Writer) error {
			t.Fatal("invalid apply flags must not run detection or execution")
			return nil
		})
		command.SetArgs(args)
		command.SetOut(io.Discard)
		command.SetErr(io.Discard)
		if err := command.ExecuteContext(t.Context()); err == nil {
			t.Fatalf("%v accepted without its required companion flag", args)
		}
	}
}

func TestOnboardApplyRefusesUncoordinatedLanguagesBeforeRuntimeSetup(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("pyproject.toml", []byte("[tool.pytest.ini_options]\n"), 0600))
	command := newOnboardCommand(func(io.Writer) error { t.Fatal("apply must not print unrelated instructions"); return nil })
	command.SetArgs([]string{"--apply", "--yes"})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	err := command.ExecuteContext(t.Context())
	require.ErrorContains(t, err, "--apply supports JavaScript frameworks")
	require.NotContains(t, err.Error(), "--all")
	require.NotContains(t, output.String(), "Usage:")
}

// Agents reach for --apply first; for a language whose CI setup they apply by
// hand, it returns the coordinated handoff instead of a dead end.
func TestOnboardApplyHandsManualSetupToTheCoordinator(t *testing.T) {
	for _, fixture := range []struct {
		framework string
		files     map[string]string
	}{
		{"rspec", map[string]string{"Gemfile": "source 'https://rubygems.org'\ngem 'rspec'\n", ".github/workflows/test.yml": "jobs:\n  test:\n    steps:\n      - uses: ruby/setup-ruby@v1\n        with: {ruby-version: '3.3'}\n      - run: bundle exec rspec\n"}},
		{"junit5", map[string]string{"pom.xml": `<project><dependencies><dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter</artifactId></dependency></dependencies></project>`, ".github/workflows/test.yml": "jobs:\n  test:\n    steps:\n      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - run: mvn -B verify\n"}},
	} {
		t.Run(fixture.framework, func(t *testing.T) {
			t.Chdir(t.TempDir())
			viper.Reset()
			settings.Init()
			t.Cleanup(func() { viper.Reset(); settings.Init() })
			for name, contents := range fixture.files {
				require.NoError(t, os.MkdirAll(filepath.Dir(name), 0700))
				require.NoError(t, os.WriteFile(name, []byte(contents), 0600))
			}
			command := newOnboardCommand(nil)
			command.SetArgs([]string{"--apply", "--yes", "--agent"})
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(io.Discard)
			require.Error(t, command.ExecuteContext(t.Context()))
			var state onboard.AgentResult
			require.NoError(t, json.Unmarshal(output.Bytes(), &state), output.String())
			require.Equal(t, "ci_setup_required", state.Code, "the agent applies the CI setup, as with onboard --agent")
			require.Equal(t, []string{"testdrive", "--framework", fixture.framework, "--all", "--agent", "--yes"}, state.ResumeCommand[1:7])
		})
	}
}

func TestAgentHelpOmitsUnrelatedPlannerFlags(t *testing.T) {
	command := newOnboardCommand(func(io.Writer) error { return nil })
	command.SetArgs([]string{"--help"})
	var output bytes.Buffer
	command.SetOut(&output)
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "--facts") || !strings.Contains(text, "--framework") || strings.Contains(text, "--ci-job-overhead") {
		t.Fatalf("agent help is missing workflow flags or includes unrelated tuning: %s", text)
	}
}
