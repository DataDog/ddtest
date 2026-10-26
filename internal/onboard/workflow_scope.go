package onboard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/kballard/go-shellquote"
)

const PreparationNeedsFacts PreparationKind = "unresolved-prerequisite"

// Workflow discovery can ask for explicit setup facts for a local Node or
// shell script.
// Ordinary discovery remains conservative. No script is executed or declared a
// build until the caller supplies expected outputs for that exact prerequisite.
func DiscoverWorkflowScope(root, framework string) (ValidationScope, error) {
	return discoverValidationScope(root, framework, true)
}

func classifyFactPrerequisites(root, directory, command, framework string, resolution commandResolution) commandResolution {
	if resolution.Reason == "" || resolution.Review {
		return resolution
	}
	fragments, err := staticCommands(command)
	if err != nil {
		return resolution
	}
	var builds []BuildCommand
	var prerequisites [][]BuildCommand
	reason := ""
	for _, words := range fragments {
		remaining := 256
		part := resolveJestSequence(root, directory, shellquote.Join(words...), nil, &remaining, framework)
		if part.Reason != "" {
			if !localNodeSetup(root, directory, words, 0) {
				return resolution
			}
			reason = joinDiagnostic(reason, part.Reason)
			builds = append(builds, BuildCommand{Directory: repositoryPath(root, directory), Command: shellquote.Join(words...), Kind: PreparationNeedsFacts})
			continue
		}
		for _, test := range part.Tests {
			prerequisites = append(prerequisites, append(append([]BuildCommand{}, builds...), test.Prerequisites...))
		}
		builds = append(builds, part.Builds...)
	}
	if reason == "" || reason != resolution.Reason || len(prerequisites) != len(resolution.Tests) {
		return resolution
	}
	resolution.Reason = ""
	resolution.Builds = builds
	for i := range resolution.Tests {
		resolution.Tests[i].UnvalidatedSetup = filterFactDiagnostic(resolution.Tests[i].UnvalidatedSetup, reason)
		resolution.Tests[i].Prerequisites = prerequisites[i]
	}
	return resolution
}

func filterFactDiagnostic(values []string, reason string) []string {
	var out []string
	for _, value := range values {
		if value != reason {
			out = append(out, value)
		}
	}
	return out
}

func localNodeSetup(root, directory string, words []string, depth int) bool {
	if len(words) == 0 || depth > 16 {
		return false
	}
	// A repository shell script, run directly or through sh/bash, is setup the
	// caller can declare like a Node script.
	if strings.HasPrefix(words[0], "./") {
		info, ok := repositoryFile(root, directory, words[0])
		return ok && info.Mode().Perm()&0o111 != 0
	}
	if len(words) < 2 {
		return false
	}
	if (words[0] == "sh" || words[0] == "bash") && !strings.HasPrefix(words[1], "-") && filepath.IsLocal(words[1]) {
		_, ok := repositoryFile(root, directory, words[1])
		return ok
	}
	if (words[0] == "node" || words[0] == "babel-node") && !strings.HasPrefix(words[1], "-") && filepath.IsLocal(words[1]) {
		_, ok := repositoryFile(root, directory, words[1])
		return ok
	}
	if len(words) != 3 || words[1] != "run" || (words[0] != "npm" && words[0] != "pnpm" && words[0] != "yarn") {
		return false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	data, err := os.ReadFile(filepath.Join(directory, "package.json"))
	if err != nil || json.Unmarshal(data, &pkg) != nil {
		return false
	}
	commands, err := staticCommands(pkg.Scripts[words[2]])
	return err == nil && len(commands) == 1 && localNodeSetup(root, directory, commands[0], depth+1)
}

// repositoryFile resolves name from directory and reports whether it is a
// regular file inside the repository, following symlinks.
func repositoryFile(root, directory, name string) (os.FileInfo, bool) {
	path, err := filepath.EvalSymlinks(filepath.Join(directory, name))
	if err != nil {
		return nil, false
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, false
	}
	relative, err := filepath.Rel(canonicalRoot, path)
	if err != nil || !filepath.IsLocal(relative) {
		return nil, false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	return info, true
}

type workflowSetupScopeKey struct{}
type workflowSetupScope struct {
	root  string
	scope ValidationScope
}

// Validated facts affect setup classification only. Runtime versions, conditions
// and instrumentation checks still use the original workflow and resolver.
func WithWorkflowSetupScope(ctx context.Context, root string, scope ValidationScope) context.Context {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		canonical = root
	}
	return context.WithValue(ctx, workflowSetupScopeKey{}, workflowSetupScope{canonical, scope})
}

func resolveFactBackedRuntime(ctx context.Context, root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, framework string, resolution commandResolution) commandResolution {
	binding, ok := ctx.Value(workflowSetupScopeKey{}).(workflowSetupScope)
	canonical, err := filepath.EvalSymlinks(root)
	if !ok || err != nil || canonical != binding.root {
		return resolution
	}
	directory, err := repositoryDirectory(root, root, stepDirectory(workflow, job, step))
	if err != nil {
		return resolution
	}
	classified := classifyFactPrerequisites(root, directory, step.Run, framework, resolution)
	if classified.Reason != "" {
		return resolution
	}
	for _, build := range classified.Builds {
		if build.Kind != PreparationNeedsFacts {
			continue
		}
		found, activeConsumer, excludedConsumer := false, false, false
		for _, entry := range binding.scope.Commands {
			for _, accepted := range entry.Prerequisites {
				if accepted.Kind != PreparationNeedsFacts || accepted.Command != build.Command || accepted.Directory != build.Directory {
					continue
				}
				activeConsumer = activeConsumer || !entry.CIInstrumentationExcluded
				excludedConsumer = excludedConsumer || entry.CIInstrumentationExcluded
				found = found || (accepted.FactSource != "" && len(accepted.ExpectedFiles) > 0)
			}
		}
		// Excluded consumers never execute locally. Shared prerequisites still
		// need accepted facts when any active configuration consumes them.
		if !found && (activeConsumer || !excludedConsumer) {
			return resolution
		}
	}
	return classified
}
