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
// each shell in the workspace, apart from the plugin's .datadog folder that host
// stages of the same workspace use and from the shells that run at the same
// time in that workspace (the parallel branches under one Docker agent),
// and tests the key without printing it in Jenkins' shell trace (sh -x).
// The key comes from a Jenkins credential the stage binds: the plugin's own
// key reaches only the steps in its scope.

// jenkinsTracerFolder is where the snippet installs the tracers: a new
// folder in the workspace, unique to the shell even across containers that
// share the workspace (a PID is not: each container has its own PID
// namespace), and not in the container's /tmp, which hardened images may
// mount noexec, stopping the tracer's native add-ons. When mktemp fails, the
// empty folder fails the snippet's mkdir, so it logs that the installation
// failed.
const jenkinsTracerFolder = `$(mktemp -d "$PWD/.datadog-ci-container.XXXXXX")`

// jenkinsTracerIgnore is the .gitignore entry for the folders the snippet
// leaves in the workspace: unanchored, since the snippet creates them in the
// sh step's working directory, which dir(…) or a cd may put below the
// checkout root.
const jenkinsTracerIgnore = ".datadog-ci-container.*/"

// setupIgnores keeps the snippet's folders untracked when edits write a
// Jenkinsfile that runs the setup snippet.
func (jenkins) setupIgnores(edits []setupEdit) []string {
	if slices.ContainsFunc(edits, func(edit setupEdit) bool { return strings.Contains(string(edit.after), jenkinsTracerFolder) }) {
		return []string{jenkinsTracerIgnore}
	}
	return nil
}

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
// script see its exports, other sh steps never do. Where a datadog(…) call
// ddtest cannot read may reach the stage, the plugin may run beside the
// snippet, in the same workspace.
func (c jenkinsContainer) setupTerms() ciSetupTerms {
	terms := ciSetupTerms{
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
	if c.reset {
		terms.Beside = "the " + jenkinsPluginReference + ", which a datadog(…) call ddtest cannot read may enable"
	}
	return terms
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
	text.WriteString("2. In every sh script that runs the tests in those stages (each sh step is a shell of its own, so a stage whose tests run in several sh steps needs them in each), add these lines immediately before the test command, after dependency installation and any cd into the test directory, inside the same sh string. Write that script as a single-quoted Groovy string (sh '''…''') so Groovy leaves every $ to the shell, and keep its other commands unchanged:\n\n```sh\n")
	text.WriteString(jenkinsSnippet(request, c.reset).shell())
	text.WriteString("\n```\n")
	text.WriteString("\nThe snippet installs the tracers into a new folder of the workspace for each shell (" + jenkinsTracerFolder + "), apart from the .datadog folder of the Datadog Jenkins plugin and from the shells that run at the same time in the same workspace, such as parallel branches under one Docker agent. The folders stay in the workspace after the build, in the working directory of each sh step: add " + jenkinsTracerIgnore + " to the repository .gitignore, without a leading /, so they stay untracked below the checkout root too, and a workspace cleanup (such as cleanWs()) removes them. It tests the key with ${DD_API_KEY:+set}, so Jenkins' shell trace (sh -x) never prints it. The installer needs Bash and curl or wget in the container's image: images without Bash (Alpine variants, BusyBox, distroless) or curl (official -slim images) need another image, or a Dockerfile that installs them.\n")
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

// jenkinsBuildArgs reads the docker build options of an agent { dockerfile
// … }'s additionalBuildArgs: the build arguments it overrides, whose values
// ddtest leaves unknown, and the stage --target selects. problem explains
// options ddtest cannot follow: shell quoting, escapes and expansions,
// which Jenkins' sh step applies before docker build reads the words, or
// another Dockerfile (-f, --file).
func jenkinsBuildArgs(value string) (target string, overridden map[string]bool, problem string) {
	if strings.ContainsAny(value, "\"'\\") {
		return "", nil, "its additionalBuildArgs " + value + " use shell quoting or escapes, which ddtest cannot resolve"
	}
	overridden = map[string]bool{}
	words := strings.Fields(value)
	for i := 0; i < len(words); i++ {
		word := words[i]
		name, inline, hasValue := strings.Cut(word, "=")
		if !strings.HasPrefix(word, "--") {
			// Short options never take their value after =.
			name, inline, hasValue = word, "", false
		}
		option := ""
		switch name {
		case "--build-arg", "--target", "-f", "--file":
			option = inline
			if !hasValue {
				if i+1 >= len(words) {
					return "", nil, "its additionalBuildArgs end with " + name + " and no value"
				}
				i++
				option = words[i]
			}
		}
		// A build argument's value is unknown anyway; anything else the
		// shell expands may be any option.
		read := word
		if name == "--build-arg" {
			read, _, _ = strings.Cut(option, "=")
		} else if option != "" {
			read = option
		}
		switch {
		case strings.ContainsAny(read, "$`"):
			return "", nil, "its additionalBuildArgs " + value + " read the shell's environment, which ddtest cannot resolve"
		case name == "--build-arg":
			overridden[read] = true
		case name == "--target":
			target = option
		case name == "-f" || name == "--file":
			return "", nil, "its additionalBuildArgs select another Dockerfile (" + name + ")"
		}
	}
	return target, overridden, ""
}

// jenkinsDockerfileAgent reads the image an agent { dockerfile … } builds,
// from its filename and dir (the checkout root's Dockerfile by default),
// and the stage its additionalBuildArgs' --target selects, with the build
// arguments they override left unknown.
func (r *jenkinsReader) jenkinsDockerfileAgent(call *groovyCall, scope jenkinsScope) jenkinsAgent {
	agent := jenkinsAgent{kind: "container", describe: "agent { dockerfile … }"}
	file, directory, target := "Dockerfile", ".", ""
	overridden := map[string]bool{}
	buildProblem := ""
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
				if problem != "" {
					buildProblem = "its additionalBuildArgs " + r.text(args[0]) + " cannot be resolved"
					continue
				}
				target, overridden, buildProblem = jenkinsBuildArgs(value)
			}
		}
	}
	dockerfile := path.Clean(path.Join(directory, file))
	agent.describe = "agent { dockerfile … } (" + dockerfile + ")"
	agent.dockerfile = dockerfile
	if buildProblem != "" {
		agent.imageProblem = buildProblem
		return agent
	}
	if strings.HasPrefix(dockerfile, "../") || path.IsAbs(dockerfile) {
		agent.imageProblem = "its Dockerfile " + dockerfile + " is outside the checkout"
		return agent
	}
	data, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(dockerfile)))
	if err != nil {
		agent.imageProblem = "its Dockerfile " + dockerfile + " cannot be read"
		return agent
	}
	read := readBuildkiteDockerfile(string(data), target, overridden)
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
	case r.pluginMayReach(scope) && jenkinsResetMissing(setup):
		return "ddtest cannot tell whether the datadog(…) call " + r.pluginUnread(scope) + " reaches this step, whose variables the setup snippet would then run with. Add these statements immediately before the snippet, as ddtest's instructions show, so it removes them first:\n" + strings.Join(jenkinsPluginResets(strings.Fields(setup.Inputs["languages"])...), "\n")
	case cleared:
		return "An earlier command of the sh script clears DD_API_KEY (export DD_API_KEY= or unset), so the setup snippet always skips; remove it."
	case key == jenkinsSecret("DD_API_KEY") && scope.keyBinding != "":
		return "withCredentials binds DD_API_KEY with " + scope.keyBinding + ", which sets it to a file's path or another field of the credential, not the Datadog API key, so the tests would report with an invalid key: store the key as a Jenkins credential of kind Secret text and bind it with withCredentials(" + jenkinsCredentialBinding + ") { … } or credentials('" + jenkinsCredentialID + "')."
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

// jenkinsResetMissing reports a setup snippet that does not first run the
// reset of every language it selects (see jenkinsPluginResets): another
// language's reset leaves the selected one's plugin variables in place.
func jenkinsResetMissing(setup ciSetup) bool {
	return slices.ContainsFunc(jenkinsPluginResets(strings.Fields(setup.Inputs["languages"])...), func(reset string) bool {
		expected, parsed := parseCIShell(reset)
		return !parsed || len(expected.Stmts) != 1 || !slices.Contains(setup.before, shellText(expected.Stmts[0]))
	})
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
