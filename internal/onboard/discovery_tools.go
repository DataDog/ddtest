package onboard

import (
	"github.com/kballard/go-shellquote"
	"slices"
	"strings"
)

type discoveryTool string

const (
	metadataTool  discoveryTool = "metadata"
	buildTool     discoveryTool = "build"
	reviewTool    discoveryTool = "review"
	frameworkTool discoveryTool = "framework"
)

// These are classifications of explicit executable names, not proofs about
// project configuration. Plugin-capable tools remain visible for review, and
// unknown wrappers never become safe just because their script is named build.
var discoveryTools = map[string]discoveryTool{
	"echo":        metadataTool,  // Shell builtin: prints its already-inspected literal arguments.
	"printf":      metadataTool,  // Shell builtin: formats literal arguments; substitutions are inspected separately.
	"true":        metadataTool,  // Shell builtin with no child command.
	"false":       metadataTool,  // Shell builtin with no child command.
	"mkdir":       metadataTool,  // Creates directories; does not dispatch a test runner.
	"cp":          metadataTool,  // Copies files; mutations are retained as setup diagnostics.
	"jq":          reviewTool,    // Transforms data; only literal filters/paths are accepted for redirection.
	"eslint":      reviewTool,    // Linter; plugins/configuration need separate review.
	"standard":    reviewTool,    // Linter; configuration needs separate review.
	"prettier":    reviewTool,    // Formatter; plugins/configuration need separate review.
	"oxlint":      reviewTool,    // Linter; configuration needs separate review.
	"oxfmt":       reviewTool,    // Common formatter; configuration needs separate review.
	"flow":        reviewTool,    // Type checker; not a Jest entry point.
	"codecov":     reviewTool,    // Coverage uploader; not a Jest entry point.
	"docusaurus":  reviewTool,    // Documentation builder; plugins/configuration need separate review.
	"vitepress":   reviewTool,    // Documentation builder; plugins/configuration need separate review.
	"tsc":         buildTool,     // TypeScript compiler; records the build prerequisite.
	"tsgo":        buildTool,     // TypeScript compiler; records the build prerequisite.
	"rollup":      buildTool,     // Bundler; plugins/configuration need separate review.
	"webpack":     buildTool,     // Bundler; plugins/configuration need separate review.
	"tsup":        buildTool,     // Bundler; plugins/configuration need separate review.
	"unbuild":     buildTool,     // Bundler; plugins/configuration need separate review.
	"jest":        frameworkTool, // Explicit test runner; matched before this table for the selected framework.
	"vitest":      frameworkTool, // Explicit different test runner; outside Jest scope.
	"playwright":  frameworkTool, // Explicit different test runner; outside Jest scope.
	"mocha":       frameworkTool, // Explicit different test runner; outside Jest scope.
	"cypress":     frameworkTool, // Explicit different test runner; outside Jest scope.
	"cucumber-js": frameworkTool, // Explicit different test runner; outside Jest scope.
	"tap":         frameworkTool, // Explicit different test runner; outside Jest scope.
	"mochify":     frameworkTool, // Explicit different test runner; outside Jest scope.
}

func resolveDiscoveryTool(root, directory string, words []string, framework string, tool discoveryTool) commandResolution {
	command := shellquote.Join(words...)
	switch tool {
	case metadataTool:
		if words[0] == "printf" && len(words) > 1 && words[1] == "-v" {
			return unresolvedCommand("Shell variable assignment requires review: " + command)
		}
		return commandResolution{}
	case frameworkTool:
		return commandResolution{OtherFramework: true, ReviewReason: "Other test framework is outside " + framework + " validation: " + command}
	case buildTool:
		if (words[0] == "tsc" || words[0] == "tsgo") && slices.Contains(words[1:], "--noEmit") {
			break
		}
		result := commandResolution{Builds: []BuildCommand{{Directory: repositoryPath(root, directory), Command: command}}}
		if words[0] != "tsc" && words[0] != "tsgo" {
			result.ReviewReason = "Build/tool configuration requires separate review: " + command
		}
		return result
	}
	return commandResolution{ReviewReason: "Tool configuration is outside " + framework + " validation; review separately: " + strings.Join(words, " ")}
}
