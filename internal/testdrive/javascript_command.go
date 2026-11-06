package testdrive

import (
	"fmt"
	"slices"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/kballard/go-shellquote"
)

type javascriptCommandRequest struct {
	root, framework, override string
	checkOnly                 bool
	scope                     onboard.ValidationScope
	scopeErr                  error
}

// javascriptCommandSelection retains the invocation's plan and environment,
// which are needed to credit the matching CI configuration after selection.
type javascriptCommandSelection struct {
	command     string
	args        []string
	plan        onboard.TestCommand
	environment map[string]string
}

// selectJavaScriptCommand keeps the fallback until discovery supplies a valid
// invocation. Its errors are deferred until Run so startup recovery can replace
// an earlier report even when command discovery fails.
func selectJavaScriptCommand(request javascriptCommandRequest, command string, args []string) (javascriptCommandSelection, error) {
	result := javascriptCommandSelection{command: command, args: args}
	var selected string
	var err error
	switch request.framework {
	case "jest":
		if request.checkOnly {
			var entry onboard.TestCommand
			entry, err = onboard.JestConfigurationCommand(request.root, request.override)
			selected, result.environment = entry.Command, entry.Environment
		} else {
			if request.override != "" {
				result.plan, err = onboard.ForwardJestExecution(request.root, request.override)
			} else {
				result.plan, err = onboard.JestExecutionCommand(request.root)
			}
			selected = result.plan.Command
		}
	case "playwright":
		selected, err = onboard.PlaywrightValidationCommandInScope(request.root, request.override, request.scope, request.scopeErr)
	case "cypress":
		result.plan, err = onboard.CypressExecutionCommandInScope(request.root, request.override, request.scope, request.scopeErr)
		selected = result.plan.Command
	case "cucumber":
		result.plan, err = onboard.CucumberExecutionCommand(request.root, request.override)
		selected = result.plan.Command
	case "webdriverio":
		selected, err = onboard.WebdriverIOValidationCommand(request.root, request.override)
	case "mocha":
		selected, err = onboard.MochaValidationCommandInScope(request.root, request.override, request.scope, request.scopeErr)
	case "vitest":
		result.plan, err = onboard.VitestValidationCommandInScope(request.root, request.override, request.scope, request.scopeErr)
		selected = result.plan.Command
	default:
		return result, nil
	}
	// Jest can return a usable invocation with a discovery finding. Keep both;
	// the other selectors return no executable invocation on an error.
	if err != nil && request.framework != "jest" {
		return result, err
	}
	if request.framework == "cypress" && result.plan.Cypress != nil {
		result.command, result.args = "node", nil
		return result, nil
	}
	if request.framework == "vitest" {
		result.environment = result.plan.Environment
	}
	// Cypress and Vitest require an explicit resolved invocation. Other
	// frameworks may intentionally fall back to their default command.
	if selected == "" && request.framework != "cypress" && request.framework != "vitest" {
		return result, err
	}
	words, splitErr := shellquote.Split(selected)
	if splitErr != nil || len(words) == 0 {
		return result, fmt.Errorf("invalid %s command: %s", displayName(request.framework), selected)
	}
	result.command, result.args = words[0], words[1:]
	if request.framework == "jest" && result.command == "npx" && !slices.Contains(result.args, "--no-install") {
		result.args = append([]string{"--no-install"}, result.args...)
	}
	return result, err
}
