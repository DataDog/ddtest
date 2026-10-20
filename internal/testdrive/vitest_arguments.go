package testdrive

import (
	"fmt"
	"strings"
)

// Vitest rejects duplicate scalar options. Replace only validation-owned
// output paths and the synthetic probe's name/coverage overrides. Everything
// after the argument terminator is positional and stays untouched.
func vitestValidationArguments(args []string, probe bool) ([]string, error) {
	var result []string
	for i := 0; i < len(args); i++ {
		argument := args[i]
		if argument == "--" {
			return append(result, args[i:]...), nil
		}
		key, _, inline := strings.Cut(argument, "=")
		owned := key == "--outputFile" || key == "--outputFile.json" || key == "--coverage.reportsDirectory"
		if probe {
			owned = owned || key == "--testNamePattern" || key == "-t"
			for _, metric := range []string{"lines", "branches", "functions", "statements"} {
				owned = owned || key == "--coverage.thresholds."+metric
			}
		}
		if !owned {
			result = append(result, argument)
			continue
		}
		if !inline {
			i++
			if i >= len(args) || args[i] == "--" {
				return nil, fmt.Errorf("original Vitest option %s has no value", key)
			}
		}
	}
	return result, nil
}
