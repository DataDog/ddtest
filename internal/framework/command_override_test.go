package framework

import (
	"bytes"
	"log/slog"
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

func TestLoadCommandOverride_WithSeparator(t *testing.T) {
	tests := []struct {
		name            string
		command         string
		expected        []string
		expectWarning   bool
		warningContains string
	}{
		{
			name:            "command with -- separator only",
			command:         "bundle exec rspec --",
			expected:        []string{"bundle", "exec", "rspec"},
			expectWarning:   true,
			warningContains: "Command contains '--' separator",
		},
		{
			name:            "command with -- and files",
			command:         "bundle exec my-wrapper -- spec/file_spec.rb",
			expected:        []string{"bundle", "exec", "my-wrapper"},
			expectWarning:   true,
			warningContains: "Command contains '--' separator",
		},
		{
			name:            "command with -- and multiple files",
			command:         "bundle exec rspec -- spec/file1_spec.rb spec/file2_spec.rb",
			expected:        []string{"bundle", "exec", "rspec"},
			expectWarning:   true,
			warningContains: "Command contains '--' separator",
		},
		{
			name:            "command with flags before --",
			command:         "bundle exec rspec --profile -- spec/file_spec.rb",
			expected:        []string{"bundle", "exec", "rspec", "--profile"},
			expectWarning:   true,
			warningContains: "Command contains '--' separator",
		},
		{
			name:            "command without separator",
			command:         "bundle exec rspec",
			expected:        []string{"bundle", "exec", "rspec"},
			expectWarning:   false,
			warningContains: "",
		},
		{
			name:            "command with double dash in flag name",
			command:         "bundle exec rspec --no-profile",
			expected:        []string{"bundle", "exec", "rspec", "--no-profile"},
			expectWarning:   false,
			warningContains: "",
		},
		{
			name:            "-- at the beginning",
			command:         "-- spec/file_spec.rb",
			expected:        []string{},
			expectWarning:   true,
			warningContains: "Command contains '--' separator",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset viper and set the command
			viper.Reset()
			viper.Set("command", tt.command)
			settings.Init()

			// Capture log output
			var logBuf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{
				Level: slog.LevelWarn,
			}))
			slog.SetDefault(logger)

			result := loadCommandOverride()

			// Check result
			if len(result) != len(tt.expected) {
				t.Errorf("expected %d parts, got %d. Expected: %v, Got: %v", len(tt.expected), len(result), tt.expected, result)
				return
			}

			for i, part := range result {
				if part != tt.expected[i] {
					t.Errorf("at index %d: expected %q, got %q", i, tt.expected[i], part)
				}
			}

			// Check warning
			logOutput := logBuf.String()
			if tt.expectWarning {
				if !strings.Contains(logOutput, tt.warningContains) {
					t.Errorf("expected warning containing %q, but got: %q", tt.warningContains, logOutput)
				}
				if !strings.Contains(logOutput, tt.command) {
					t.Errorf("expected warning to include original command %q, but got: %q", tt.command, logOutput)
				}
			} else {
				if strings.Contains(logOutput, "separator") {
					t.Errorf("did not expect warning about separator, but got: %q", logOutput)
				}
			}
		})
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
			name:            "rspec with custom command and -- separator",
			command:         "bundle exec my-rspec-wrapper --",
			frameworkType:   "rspec",
			expectedCommand: "bundle",
			expectedArgs:    []string{"exec", "my-rspec-wrapper"},
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
				command, args := rspec.getRSpecCommand()

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
				command, args, _ := minitest.getMinitestCommand()

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
