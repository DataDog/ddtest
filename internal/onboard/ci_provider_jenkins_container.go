// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// The Datadog Jenkins plugin installs the tracer only on the build agent
// (see jenkinsContainerUnsupported), so a stage that runs its tests in a
// container (agent { docker }, agent { dockerfile }, agent { kubernetes },
// docker.image(…).inside, container(…)) sets Test Optimization up with
// Datadog's installer instead: the setup snippet Buildkite steps run (see
// buildkiteSnippet), in the sh step that runs the tests, since each sh step
// starts a fresh shell. Its Jenkins form keeps the tracers in a folder of
// its own, apart from the plugin's .datadog folder that host stages of the
// same workspace use, and tests the key without printing it in Jenkins'
// shell trace (sh -x). The key comes from a Jenkins credential the stage
// binds: the plugin's own key reaches only the steps in its scope.

// jenkinsTracerFolder is where the snippet installs the tracers.
const jenkinsTracerFolder = "$PWD/.datadog-ci-container"

// jenkinsSnippetForm is the setup snippet Jenkins container stages run.
var jenkinsSnippetForm = snippetForm{provider: jenkinsProvider, folder: jenkinsTracerFolder, keySet: true}

// jenkinsCredentialID is the Jenkins credential onboard --apply binds
// DD_API_KEY from, a Secret text the user stores the Datadog API key in.
const jenkinsCredentialID = "datadog-api-key"

// jenkinsCredentialBinding is the withCredentials argument that binds it.
const jenkinsCredentialBinding = "[string(credentialsId: '" + jenkinsCredentialID + "', variable: 'DD_API_KEY')]"

// jenkinsContainer is the Jenkins provider for a job that runs in a
// container, whose setup mechanism is the setup snippet (see
// runtimeJob.SetupProvider). reset reports a job ddtest cannot prove is
// outside every datadog(…) option's scope, whose snippet must first remove
// the variables the Datadog Jenkins plugin may have set (see
// jenkinsPluginResets).
type jenkinsContainer struct {
	jenkins
	reset bool
}

func (jenkinsContainer) setupName() string { return "Datadog setup snippet" }

func (jenkinsContainer) setupInput(name string) string { return buildkite{}.setupInput(name) }

func (jenkinsContainer) setupProblem(setup ciSetup, tracerInput string, exact func(string) bool, example, site string) string {
	return snippetSetupProblem(setup, tracerInput, exact, example, site)
}

func (jenkinsContainer) apiKeyAction(skipped bool) string {
	text := "Ask a Jenkins administrator to store the Datadog API key as a Jenkins credential of kind Secret text (ID " + jenkinsCredentialID + ", or the one the Datadog plugin already uses), which the container stages bind to DD_API_KEY with withCredentials or credentials(…). Never write the key into the Jenkinsfile."
	if skipped {
		text += " Until it exists, those stages log that Test Optimization is disabled and their tests run untraced."
	}
	return text
}

// apiKeyActions asks for the plugin's configuration where the plugin sets
// jobs up, and for the credential the container stages bind where the
// setup snippet does.
func (j jenkins) apiKeyActions(references []string, skipped bool) string {
	plugin := slices.Contains(references, jenkinsPluginReference) || len(references) == 0
	snippet := slices.ContainsFunc(references, func(reference string) bool { return reference != jenkinsPluginReference })
	switch {
	case plugin && snippet:
		return j.apiKeyAction(skipped) + " " + jenkinsContainer{}.apiKeyAction(skipped)
	case snippet:
		return jenkinsContainer{}.apiKeyAction(skipped)
	}
	return j.apiKeyAction(skipped)
}

// The snippet runs in the sh step's own shell: later commands of the same
// script see its exports, other sh steps never do.
func (jenkinsContainer) setupTerms() ciSetupTerms {
	return ciSetupTerms{
		Installer:  "installer",
		Entry:      "the setup snippet",
		SetupEntry: "the Datadog setup snippet",
		Later:      "later commands of the same sh script",
		Variables:  "the stage's environment directive",
		Persist: func(command string) string {
			return command + " in the same sh script, after the setup snippet"
		},
		PersistEntry:    func(command string) string { return command },
		MatrixExclusion: "with the matrix's excludes",
		SharedMatrix:    "a matrix shares the stage's steps",
		ImageGuard:      func(string, string, string) string { return "" },
	}
}

// jenkinsSnippetSetup reports a setup that is the setup snippet, not the
// plugin.
func jenkinsSnippetSetup(setup ciSetup) bool {
	return setup.Reference != jenkinsPluginReference
}

// jenkinsContainerJob reports a job that runs in a container.
func jenkinsContainerJob(job runtimeJob) bool {
	_, container := job.SetupProvider.(jenkinsContainer)
	return container
}

// jenkinsPluginResetter is a language whose variables the Datadog Jenkins
// plugin sets in a container step it reaches, and that a shell statement
// there can remove before the setup snippet runs.
type jenkinsPluginResetter interface {
	// ciPluginReset removes the plugin's variables in the step's shell,
	// keeping the step's own.
	ciPluginReset() string
}

// jenkinsPluginResets are the statements that remove the plugin's variables
// for the languages whose installer tokens are given, or for every language
// with none.
func jenkinsPluginResets(tokens ...string) []string {
	var resets []string
	for _, language := range discoveryLanguages {
		if len(tokens) > 0 && !slices.Contains(tokens, language.actionLanguage()) {
			continue
		}
		if resetter, ok := language.(jenkinsPluginResetter); ok {
			resets = append(resets, resetter.ciPluginReset())
		}
	}
	return resets
}

// jenkinsSnippet is the setup snippet ddtest prescribes for request in a
// container stage; reset adds the statements that first remove the plugin's
// variables.
func jenkinsSnippet(request ciSetupRequest, reset bool) buildkiteSnippet {
	snippet := requestSnippet(request, jenkinsSnippetForm)
	if reset {
		snippet.before = jenkinsPluginResets(request.Language)
	}
	return snippet
}

func (c jenkinsContainer) setupInstructions(request ciSetupRequest) string {
	var text strings.Builder
	stages := "the stages that run the tests in a container"
	if len(request.Jobs) > 0 {
		stages += " (" + strings.Join(request.Jobs, ", ") + ")"
	}
	text.WriteString("Set up Datadog Test Optimization in " + stages + " with Datadog's installer (" + buildkiteInstaller + "), run in the sh step that runs the tests: the Datadog Jenkins plugin installs the tracer only on the build agent, so it cannot set up tests inside a container, and every sh step starts a fresh shell, so a separate sh step's exports would never reach the tests.\n\n")
	text.WriteString("1. Ask the user to have a Jenkins administrator store the Datadog API key as a Jenkins credential of kind Secret text (ID " + jenkinsCredentialID + ", or the one the Datadog plugin already uses), and bind it to DD_API_KEY in those stages: wrap the sh step that runs the tests in withCredentials(" + jenkinsCredentialBinding + ") { … }, or set DD_API_KEY = credentials('" + jenkinsCredentialID + "') in the stage's environment directive. The Datadog Jenkins plugin's own key reaches only the steps in its option's scope. Never write the key into the repository or the Jenkinsfile. Without the key, the snippet skips the setup, logs that Test Optimization is disabled, and the tests run untraced.\n")
	text.WriteString("2. In the sh script that runs the tests in each of those stages, add these lines immediately before the test command, after dependency installation and any cd into the test directory, inside the same sh string. Write that script as a single-quoted Groovy string (sh '''…''') so Groovy leaves every $ to the shell, and keep its other commands unchanged:\n\n```sh\n")
	text.WriteString(jenkinsSnippet(request, c.reset).shell())
	text.WriteString("\n```\n")
	text.WriteString("\nThe snippet installs the tracers into " + jenkinsTracerFolder + ", apart from the .datadog folder of the Datadog Jenkins plugin, and tests the key with ${DD_API_KEY:+set}, so Jenkins' shell trace (sh -x) never prints it. The installer needs Bash and curl or wget in the container's image: images without Bash (Alpine variants, BusyBox, distroless) or curl (official -slim images) need another image, or a Dockerfile that installs them.\n")
	text.WriteString("\n3. Keep those stages out of every datadog(…) option's scope: in a declarative pipeline, that option goes in the options of the host-agent stages that run the tests, not the pipeline's; in a scripted pipeline, it wraps only node blocks on host agents. ddtest does not certify a container stage the plugin's variables reach. Enabling Test Optimization in the job's configuration page instead of the Jenkinsfile reaches every stage, so leave it off there.")
	if c.reset {
		text.WriteString(" ddtest cannot tell whether a datadog(…) call it cannot read reaches these stages, so the snippet first removes the variables the plugin would set there.")
	}
	text.WriteString("\n\n4. Set the environment and working directory the tests depend on before the snippet. After it, extend the variables it exports instead of replacing them.\n")
	for _, note := range request.Notes {
		text.WriteString("\n- " + strings.ReplaceAll(note, "\n", "\n  "))
	}
	return strings.TrimSpace(text.String())
}

// jenkinsDockerfileAgent reads the image an agent { dockerfile … } builds,
// from its filename and dir (the checkout root's Dockerfile by default),
// with the build arguments its additionalBuildArgs override left unknown.
func (r *jenkinsReader) jenkinsDockerfileAgent(call *groovyCall, scope jenkinsScope) jenkinsAgent {
	agent := jenkinsAgent{kind: "container", describe: "agent { dockerfile … }"}
	file, directory := "Dockerfile", "."
	overridden := map[string]bool{}
	if call.Closure != nil {
		for _, statement := range call.Closure.Body.Statements {
			inner, ok := statement.(*groovyCall)
			if !ok {
				continue
			}
			args, _ := jenkinsArgs(inner)
			if len(args) != 1 {
				continue
			}
			value, problem := r.value(args[0], scope)
			switch inner.Name {
			case "filename", "dir":
				if problem != "" {
					agent.problem = "its " + inner.Name + " is not literal"
					return agent
				}
				if inner.Name == "filename" {
					file = value
				} else {
					directory = value
				}
			case "additionalBuildArgs":
				for _, word := range strings.Fields(value) {
					name, _, _ := strings.Cut(strings.TrimPrefix(word, "--build-arg="), "=")
					overridden[name] = true
				}
			}
		}
	}
	dockerfile := path.Clean(path.Join(directory, file))
	agent.describe = "agent { dockerfile … } (" + dockerfile + ")"
	agent.dockerfile = dockerfile
	if strings.HasPrefix(dockerfile, "../") || path.IsAbs(dockerfile) {
		agent.imageProblem = "its Dockerfile " + dockerfile + " is outside the checkout"
		return agent
	}
	data, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(dockerfile)))
	if err != nil {
		agent.imageProblem = "its Dockerfile " + dockerfile + " cannot be read"
		return agent
	}
	read := readBuildkiteDockerfile(string(data), "", overridden)
	if read.problem != "" {
		agent.imageProblem = "its Dockerfile " + dockerfile + ", " + read.problem
		return agent
	}
	agent.image, agent.bash, agent.downloader = read.image, read.bash, read.downloader
	return agent
}

// jenkinsImageSetupProblem explains why a container agent's image cannot
// run the installer (see buildkiteExecutor.imageSetupProblem): one known
// to lack Bash, or curl and wget. curl reports an earlier statement of the
// script that installed curl. An image ddtest cannot read is a caveat
// instead (see jenkinsUnreadImageCaveat).
func jenkinsImageSetupProblem(agent jenkinsAgent, curl bool) string {
	if agent.image == "" {
		return ""
	}
	executor := buildkiteExecutor{image: agent.image, dockerfile: agent.dockerfile, bash: agent.bash, downloader: agent.downloader}
	return executor.imageSetupProblem(curl)
}

// jenkinsUnreadImageCaveat is what ddtest cannot check about a container
// whose image it does not read.
func jenkinsUnreadImageCaveat(agent jenkinsAgent) string {
	reason := "ddtest does not read its image"
	if agent.imageProblem != "" {
		reason = agent.imageProblem
	}
	return "The setup snippet runs in " + agent.describe + ", and " + reason + ": ddtest took its runtime from the user (--ci-runtime) and could not check that the image has the Bash and the curl or wget the installer needs. Without them, the snippet logs that Test Optimization could not be installed, and the tests run untraced."
}

// jenkinsPluginOutsideCaveat is what ddtest could not check about a container
// stage it found outside every datadog(…) option's scope.
const jenkinsPluginOutsideCaveat = "ddtest checked that no datadog(…) option of the Jenkinsfile reaches the container stages that run the setup snippet; enabling Test Optimization in the job's configuration page instead would reach them too, so it must stay off there."

// snippetProblem explains why the setup snippet an sh step of scope runs
// cannot set up its tests, before what the snippet itself shows (see
// snippetSetupProblem): it must run in a container stage outside the
// plugin's scope, with DD_API_KEY bound from a Jenkins credential and an
// image that can run the installer. key is DD_API_KEY's value before it.
func (r *jenkinsReader) snippetProblem(scope jenkinsScope, setup ciSetup, key string, cleared, curl bool) string {
	switch {
	case scope.setup != nil && scope.setup.problem() == "":
		return "The setup snippet runs in the scope of the datadog(…) option at " + scope.setup.source + ", whose variables the Datadog Jenkins plugin also sets in this step: " + jenkinsContainerEffects(scope.setup.languages) + ". Keep this stage out of the option's scope (" + jenkinsContainerRemedy + "), so only the snippet sets it up."
	case r.pluginMayReach(scope) && len(setup.before) < len(jenkinsPluginResets(strings.Fields(setup.Inputs["languages"])...)):
		return "ddtest cannot tell whether the datadog(…) call " + r.pluginUnread(scope) + " reaches this step, whose variables the setup snippet would then run with. Add these statements immediately before the snippet, as ddtest's instructions show, so it removes them first:\n" + strings.Join(jenkinsPluginResets(strings.Fields(setup.Inputs["languages"])...), "\n")
	case cleared:
		return "An earlier command of the sh script clears DD_API_KEY (export DD_API_KEY= or unset), so the setup snippet always skips; remove it."
	case key == jenkinsSecret("DD_API_KEY"):
	case key == "":
		return "DD_API_KEY does not reach this sh step, so the setup snippet always skips and the tests run untraced: bind it from a Jenkins credential, with withCredentials(" + jenkinsCredentialBinding + ") { … } around the sh step or DD_API_KEY = credentials('" + jenkinsCredentialID + "') in the stage's environment directive. The Datadog Jenkins plugin's own key reaches only the steps in its option's scope."
	case unresolvedCIError(key) == nil:
		return "DD_API_KEY is written into the Jenkinsfile for this step; never commit the key: store it as a Jenkins credential of kind Secret text and bind it with withCredentials(" + jenkinsCredentialBinding + ") { … } or credentials('" + jenkinsCredentialID + "')."
	default:
		return "DD_API_KEY comes from " + key + ", which ddtest cannot tell is a Jenkins credential; bind it with withCredentials(" + jenkinsCredentialBinding + ") { … } or credentials('" + jenkinsCredentialID + "')."
	}
	return jenkinsImageSetupProblem(scope.agent, curl)
}

// pluginMayReach reports a scope ddtest cannot prove is outside every
// datadog(…) option's: a datadog(…) call it cannot read encloses it.
func (r *jenkinsReader) pluginMayReach(scope jenkinsScope) bool {
	return r.pluginUnread(scope) != ""
}

// pluginUnread names the datadog(…) call that may reach scope.
func (r *jenkinsReader) pluginUnread(scope jenkinsScope) string {
	if scope.setup != nil && scope.setup.problem() != "" {
		return "at " + scope.setup.source
	}
	return scope.pluginUnread
}
