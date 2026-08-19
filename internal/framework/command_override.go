package framework

import (
	"fmt"
	"log/slog"
	"strings"
	"unicode"

	"github.com/DataDog/ddtest/internal/settings"
)

func loadCommandOverride() []string {
	command := strings.TrimSpace(settings.GetCommand())
	if command == "" {
		return nil
	}

	parts, err := splitCommandLine(command)
	if err != nil {
		slog.Warn("Command contains invalid quoting and will be ignored.", "original_command", command, "error", err)
		return nil
	}
	if len(parts) == 0 {
		return nil
	}

	// Check for -- separator and remove it along with everything after it
	for i, part := range parts {
		if part == "--" {
			slog.Warn("Command contains '--' separator which causes ddtest-added flags to be misinterpreted. The '--' separator and anything after it will be removed. ddtest will automatically provide test files and flags.", "original_command", command)
			return parts[:i]
		}
	}

	return parts
}

// splitCommandLine separates a command into arguments using shell-style quote
// and escape handling. Commands are executed directly rather than through a
// shell, so quotes group values but are not included in the resulting args.
func splitCommandLine(command string) ([]string, error) {
	var (
		parts        []string
		current      strings.Builder
		quote        rune
		escaped      bool
		argumentOpen bool
	)

	for _, character := range command {
		if escaped {
			current.WriteRune(character)
			escaped = false
			argumentOpen = true
			continue
		}

		if quote != 0 {
			switch character {
			case quote:
				quote = 0
			case '\\':
				if quote == '"' {
					escaped = true
				} else {
					current.WriteRune(character)
				}
			default:
				current.WriteRune(character)
			}
			argumentOpen = true
			continue
		}

		switch {
		case character == '\'' || character == '"':
			quote = character
			argumentOpen = true
		case character == '\\':
			escaped = true
			argumentOpen = true
		case unicode.IsSpace(character):
			if argumentOpen {
				parts = append(parts, current.String())
				current.Reset()
				argumentOpen = false
			}
		default:
			current.WriteRune(character)
			argumentOpen = true
		}
	}

	if escaped {
		return nil, fmt.Errorf("unfinished escape sequence")
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if argumentOpen {
		parts = append(parts, current.String())
	}

	return parts, nil
}
