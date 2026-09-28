package framework

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
)

func TestLoadCommandOverride(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		expected []string
	}{
		{
			name:     "empty command",
			command:  "",
			expected: nil,
		},
		{
			name:     "whitespace only command",
			command:  "   ",
			expected: nil,
		},
		{
			name:     "simple command",
			command:  "bundle exec rspec",
			expected: []string{"bundle", "exec", "rspec"},
		},
		{
			name:     "command with flags",
			command:  "bundle exec rspec --profile",
			expected: []string{"bundle", "exec", "rspec", "--profile"},
		},
		{
			name:     "custom wrapper command",
			command:  "./custom-rspec-wrapper --custom-flag",
			expected: []string{"./custom-rspec-wrapper", "--custom-flag"},
		},
		{
			name:     "single-quoted multiword value",
			command:  "pnpm exec cucumber-js --tags 'not @slow'",
			expected: []string{"pnpm", "exec", "cucumber-js", "--tags", "not @slow"},
		},
		{
			name:     "double-quoted multiword value",
			command:  `cucumber-js --name "checkout flow"`,
			expected: []string{"cucumber-js", "--name", "checkout flow"},
		},
		{
			name:     "double-quoted regular expression",
			command:  `cucumber-js --name "^I see \d+ items$"`,
			expected: []string{"cucumber-js", "--name", `^I see \d+ items$`},
		},
		{
			name:     "escaped space",
			command:  `cucumber-js --name checkout\ flow`,
			expected: []string{"cucumber-js", "--name", "checkout flow"},
		},
		{
			name:     "empty quoted value",
			command:  `cucumber-js --name ""`,
			expected: []string{"cucumber-js", "--name", ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset viper and set the command
			viper.Reset()
			viper.Set("command", tt.command)
			settings.Init()

			result := loadCommandOverride()

			if len(result) != len(tt.expected) {
				t.Errorf("expected %d parts, got %d. Expected: %v, Got: %v", len(tt.expected), len(result), tt.expected, result)
				return
			}

			for i, part := range result {
				if part != tt.expected[i] {
					t.Errorf("at index %d: expected %q, got %q", i, tt.expected[i], part)
				}
			}
		})
	}
}

func TestLoadCommandOverride_InvalidQuotingPreservesPreviousBehavior(t *testing.T) {
	const command = `cucumber-js --publish-token "secret-token`
	t.Cleanup(func() {
		viper.Reset()
		settings.Init()
	})
	viper.Reset()
	viper.Set("command", command)
	settings.Init()

	var logBuf bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	result := loadCommandOverride()
	expected := strings.Fields(command)
	if !slices.Equal(result, expected) {
		t.Fatalf("loadCommandOverride() = %v, want %v", result, expected)
	}
	if logOutput := logBuf.String(); !strings.Contains(logOutput, "invalid quoting") {
		t.Fatalf("expected invalid quoting warning, got %q", logOutput)
	} else if strings.Contains(logOutput, "secret-token") {
		t.Fatalf("warning exposed command contents: %q", logOutput)
	}
}

func TestLoadCommandOverride_Integration(t *testing.T) {
	// Test that the command override is properly used by framework implementations
	tests := []struct {
		name            string
		command         string
		frameworkType   string
		expectedCommand string
		expectedArgs    []string
	}{
		{
			name:            "rspec with custom command",
			command:         "bundle exec my-rspec-wrapper",
			frameworkType:   "rspec",
			expectedCommand: "bundle",
			expectedArgs:    []string{"exec", "my-rspec-wrapper"},
		},
		{
			name: "rspec with separator", command: "bundle exec my-rspec-wrapper --", frameworkType: "rspec", expectedCommand: "bundle", expectedArgs: []string{"exec", "my-rspec-wrapper", "--"},
		},
		{
			name:            "minitest with custom command",
			command:         "./custom-minitest-runner --flag",
			frameworkType:   "minitest",
			expectedCommand: "./custom-minitest-runner",
			expectedArgs:    []string{"--flag"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset viper and set the command
			viper.Reset()
			viper.Set("command", tt.command)
			settings.Init()

			// Cleanup after test
			defer func() {
				viper.Reset()
				settings.Init()
			}()

			// Capture log output to suppress warnings in test output
			var logBuf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{
				Level: slog.LevelWarn,
			}))
			slog.SetDefault(logger)

			switch tt.frameworkType {
			case "rspec":
				rspec := NewRSpec()
				command, args := rspec.Command()

				if command != tt.expectedCommand {
					t.Errorf("expected command %q, got %q", tt.expectedCommand, command)
				}

				if len(args) != len(tt.expectedArgs) {
					t.Errorf("expected %d args, got %d. Expected: %v, Got: %v", len(tt.expectedArgs), len(args), tt.expectedArgs, args)
					return
				}

				for i, arg := range args {
					if arg != tt.expectedArgs[i] {
						t.Errorf("at index %d: expected %q, got %q", i, tt.expectedArgs[i], arg)
					}
				}
			case "minitest":
				minitest := NewMinitest()
				command, args, _ := minitest.getMinitestCommand(context.Background())

				if command != tt.expectedCommand {
					t.Errorf("expected command %q, got %q", tt.expectedCommand, command)
				}

				if len(args) != len(tt.expectedArgs) {
					t.Errorf("expected %d args, got %d. Expected: %v, Got: %v", len(tt.expectedArgs), len(args), tt.expectedArgs, args)
					return
				}

				for i, arg := range args {
					if arg != tt.expectedArgs[i] {
						t.Errorf("at index %d: expected %q, got %q", i, tt.expectedArgs[i], arg)
					}
				}
			}
		})
	}
}

func TestFrameworkCommand(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", t.TempDir())
	viper.Reset()
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	for _, tc := range []struct {
		framework Framework
		command   string
		args      []string
	}{
		{NewJest(), "npx", []string{"jest"}},
		{NewMocha(), "npx", []string{"mocha"}},
		{NewCucumber(), "npx", []string{"cucumber-js"}},
		{NewVitest(), "npx", []string{"vitest", "run"}},
		{NewPlaywright(), "npx", []string{"playwright", "test"}},
		{NewCypress(), "npx", []string{"cypress", "run"}},
		{NewPytest(), "python", []string{"-m", "pytest"}},
		{NewRSpec(), "bundle", []string{"exec", "rspec"}},
		{NewMinitest(), "bundle", []string{"exec", "rake", "test"}},
	} {
		t.Run(tc.framework.Name(), func(t *testing.T) {
			command, args := tc.framework.Command()
			if command != tc.command || !slices.Equal(args, tc.args) {
				t.Fatalf("Command() = %q %q, want %q %q", command, args, tc.command, tc.args)
			}
		})
	}
	for _, tc := range []struct {
		framework Framework
		path      string
		args      []string
	}{
		{NewJest(), binJestPath, nil},
		{NewRSpec(), binRSpecPath, nil},
	} {
		if err := os.MkdirAll(filepath.Dir(tc.path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(tc.path, []byte("#!/bin/sh\nexit 99\n"), 0755); err != nil {
			t.Fatal(err)
		}
		command, args := tc.framework.Command()
		if command != tc.path || !slices.Equal(args, tc.args) {
			t.Fatalf("local Command() = %q %q, want %q %q", command, args, tc.path, tc.args)
		}
	}
}

func TestFrameworkCommandPreservesOverride(t *testing.T) {
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	viper.Reset()
	viper.Set("command", `npm test -- --runInBand "path with spaces"`)
	settings.Init()
	for _, f := range []Framework{NewJest(), NewMocha(), NewCucumber(), NewVitest(), NewPlaywright(), NewCypress(), NewPytest(), NewRSpec(), NewMinitest()} {
		command, args := f.Command()
		if command != "npm" || !slices.Equal(args, []string{"test", "--", "--runInBand", "path with spaces"}) {
			t.Fatalf("%s: %s %q", f.Name(), command, args)
		}
	}
}
