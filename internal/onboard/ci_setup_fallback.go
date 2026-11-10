// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Datadog's official installer (buildkiteInstaller), which the GitLab setup
// script, the CircleCI orb and the setup snippet of Buildkite steps and
// Jenkins container stages all run, is a Bash script downloaded with curl or
// wget; the orb also exports through $BASH_ENV, which only Bash reads. ddtest
// never changes a CI image, so a job whose image lacks Bash, or both curl and
// wget, gets the fallback setup instead: POSIX sh that installs the same
// tracer release with the job's own runtime tools (npm, pip, bundler; for
// Java, curl or wget, or else the JDK) and exports what install_test_visibility_v15.sh prints for its
// language, plus its common variables, in the shell that runs the tests.
// Two comment lines above it say why, and recommend that the user add Bash
// and curl to the image; ddtest's instructions, caveats and human actions
// carry the same recommendation, always as the user's action, and the final
// report adds fallbackCaveat, for every language. Its folder's path has no whitespace
// (see fallbackBase).

// fallbackSetupName names the fallback setup in findings.
const fallbackSetupName = "Datadog setup without Bash or curl"

// fallbackComment is the comment the fallback setup carries in the CI file,
// right above it; ddtest checks for it.
var fallbackComment = []string{
	"# Datadog Test Optimization: this image has no bash/curl, so Datadog's official installer can't run here.",
	"# This is an equivalent setup without them. Recommended: add bash and curl to this image and use the official installer.",
}

// fallbackUserRecommendation is what the user is told, never what an agent
// does.
const fallbackUserRecommendation = "it's recommended that they add Bash and curl to the image themselves and then switch these jobs to Datadog's official installer, which is simpler and more reliable"

// ciFallbackLanguage is a language whose tracer the fallback setup installs.
type ciFallbackLanguage interface {
	// ciFallbackInstall is the POSIX sh command that installs the tracer
	// release $DD_TRACER_VERSION_<LANGUAGE> into $DD_TRACER_FOLDER without
	// Bash, curl or wget, and the assignments of the export that follows it,
	// as the official installer prints them for the CI provider named.
	// Neither contains a backslash: Jenkins writes the snippet in a Groovy
	// string, which would read it as an escape. A variable the export
	// extends is read as ${NAME:-} or ${NAME:+...}: the job's shell may have
	// set -u, and the variable may be unset there.
	ciFallbackInstall(provider string) (install, exports string)
	// ciFallbackTool names what the install uses, such as npm.
	ciFallbackTool() string
	// ciFallbackUnavailable explains why the fallback setup cannot run in
	// image; empty when it can.
	ciFallbackUnavailable(image string) string
}

// fallbackLanguage is the language whose installer token is token.
func fallbackLanguage(token string) (ciFallbackLanguage, bool) {
	for _, language := range discoveryLanguages {
		if language.actionLanguage() == token {
			fallback, ok := language.(ciFallbackLanguage)
			return fallback, ok
		}
	}
	return nil, false
}

// imageMissingTools describes what image lacks for Datadog's official
// installer, such as "no Bash", or "" when it has what the installer needs or
// ddtest cannot tell. bash and downloader report an image built to have them
// (a Dockerfile that installs them); curl reports an earlier command of the
// job that installs curl.
func imageMissingTools(image string, bash, downloader, curl bool) string {
	noBash := !bash && imageWithoutBash(image)
	noDownloader := !downloader && !curl && imageWithoutDownloader(image)
	switch {
	case noBash && noDownloader:
		return "no Bash, curl or wget"
	case noBash:
		return "no Bash"
	case noDownloader:
		return "neither curl nor wget"
	}
	return ""
}

// imageWithoutShell reports an image without even a POSIX shell, where no
// CI script runs at all.
func imageWithoutShell(image string) bool {
	return strings.Contains(strings.ToLower(image), "distroless")
}

// fallbackSnippet is the fallback setup's variable parts, as shell words.
type fallbackSnippet struct {
	form snippetForm
	// language is the installer's token for the language, such as js.
	language string
	// version and site are words as written, such as "5.40.0".
	version, site string
	// before are statements that run before it, whether or not it runs (see
	// buildkiteSnippet.before); after run only with it, inside its guard.
	before, after []string
}

// fallbackRequestSnippet is the fallback setup ddtest prescribes for request
// in form.
func fallbackRequestSnippet(request ciSetupRequest, form snippetForm) fallbackSnippet {
	return fallbackSnippet{form: form, language: request.Language, version: `"` + request.TracerVersion + `"`, site: `"` + request.Site + `"`, after: request.After}
}

// fallbackFolder is the DD_TRACER_FOLDER the snippet installs into, as
// written: the official installer's default, the working directory's
// .datadog, or the form's own, below $DD_TRACER_BASE (see fallbackBase)
// instead of $PWD.
func (f snippetForm) fallbackFolder() string {
	folder := `"$PWD/.datadog"`
	if f.folder != "" {
		folder = `"` + f.folder + `"`
	}
	return strings.ReplaceAll(folder, "$PWD", "$DD_TRACER_BASE")
}

// fallbackBase are the statements that choose the directory the fallback
// setup's folder is created in, $DD_TRACER_BASE: the working directory, or
// the temporary directory when the working directory's path has whitespace.
// The variables the setup exports, such as MAVEN_OPTS, NODE_OPTIONS and
// RUBYOPT, split on whitespace, so the tracer's path in them has none.
var fallbackBase = []string{
	`DD_TRACER_BASE="$PWD"`,
	`case "$DD_TRACER_BASE" in *[[:space:]]*) DD_TRACER_BASE="${TMPDIR:-/tmp}" ;; esac`,
}

// missingKeyMessage is what the setup logs without DD_API_KEY.
func (f snippetForm) missingKeyMessage() string {
	if f.job {
		return gitLabMissingKeyMessage
	}
	return buildkiteMissingKeyMessage
}

// shell writes the fallback setup as shell text, with a literal $, comment
// first. Like the setup snippet, it installs nothing and exits nothing when
// the key is missing or the install fails, so the tests still run.
func (s fallbackSnippet) shell() string {
	language, _ := fallbackLanguage(s.language)
	install, exports := "false", ""
	if language != nil {
		install, exports = language.ciFallbackInstall(s.form.provider)
	}
	lines := append(slices.Clone(fallbackComment), s.before...)
	lines = append(lines,
		"if "+s.form.keyGuard()+"; then",
		"  "+fallbackBase[0],
		"  "+fallbackBase[1],
		"  export DD_TRACER_FOLDER="+s.form.fallbackFolder()+" DD_SITE="+s.site+` DD_CIVISIBILITY_AUTO_INSTRUMENTATION_PROVIDER="`+s.form.provider+`" `+fallbackVersionVariable(s.language)+"="+s.version,
		`  if mkdir -p "$DD_TRACER_FOLDER" &&`,
		"    "+install+"; then",
		`    export DD_CIVISIBILITY_ENABLED=true DD_CIVISIBILITY_AGENTLESS_ENABLED=true DD_ENV="${DD_ENV:-ci}"`,
	)
	if exports != "" {
		lines = append(lines, "    export "+exports)
	}
	lines = append(lines, "  else", `    echo "`+buildkiteFailedInstallMessage+`" >&2`, "  fi")
	for _, line := range s.after {
		lines = append(lines, "  "+line)
	}
	lines = append(lines, "else", `  echo "`+s.form.missingKeyMessage()+`" >&2`, "fi")
	return strings.Join(lines, "\n")
}

// fallbackVersionVariable is the variable the fallback setup pins the
// language's tracer release in: DD_TRACER_VERSION_JS, which the official
// installer prints with the release it installed.
func fallbackVersionVariable(language string) string {
	return "DD_TRACER_VERSION_" + strings.ToUpper(language)
}

// fallbackSetupAt recognizes the fallback setup in form starting at
// stmts[i] of text: its guarded statement, after the statements of before
// it starts with (see snippetSetupAt), exactly as ddtest writes it, with
// any language, release and site. The comment must stand right above it in
// text. A statement in its guarded shape that differs otherwise is a setup
// ddtest does not recognize (shaped false).
func fallbackSetupAt(text string, stmts []*syntax.Stmt, i int, known map[string]string, form snippetForm, before []string) (setup ciSetup, consumed int, ok bool) {
	start := i
	var prefix []string
	for ; i < len(stmts) && len(before) > 0; i++ {
		statement := shellText(stmts[i])
		if !slices.ContainsFunc(before, func(candidate string) bool {
			expected, parsed := parseCIShell(candidate)
			return parsed && len(expected.Stmts) == 1 && shellText(expected.Stmts[0]) == statement
		}) {
			break
		}
		prefix = append(prefix, statement)
	}
	if i >= len(stmts) {
		return ciSetup{}, 0, false
	}
	clause, isIf := stmts[i].Cmd.(*syntax.IfClause)
	if !isIf || gitLabKeyGuard(clause.Cond) != "DD_API_KEY" && !keySetGuard(clause.Cond) {
		return ciSetup{}, 0, false
	}
	// The export follows the statements that choose its folder's base
	// (see fallbackBase); the install follows it.
	first := slices.IndexFunc(clause.Then, func(stmt *syntax.Stmt) bool {
		declaration, isExport := stmt.Cmd.(*syntax.DeclClause)
		return isExport && declaration.Variant.Value == "export"
	})
	if first < 0 || first > len(fallbackBase) || len(clause.Then) < first+2 {
		return ciSetup{}, 0, false
	}
	declaration, isExport := clause.Then[first].Cmd.(*syntax.DeclClause)
	if !isExport || declaration.Variant.Value != "export" {
		return ciSetup{}, 0, false
	}
	snippet := fallbackSnippet{form: form, before: prefix}
	provider := false
	for _, assign := range declaration.Args {
		if assign.Name == nil || assign.Value == nil {
			continue
		}
		switch name := assign.Name.Value; {
		case name == "DD_SITE":
			snippet.site = shellText(assign.Value)
		case name == "DD_CIVISIBILITY_AUTO_INSTRUMENTATION_PROVIDER":
			provider = true
		case strings.HasPrefix(name, "DD_TRACER_VERSION_"):
			snippet.language = strings.ToLower(strings.TrimPrefix(name, "DD_TRACER_VERSION_"))
			snippet.version = shellText(assign.Value)
		}
	}
	if !provider || snippet.language == "" {
		return ciSetup{}, 0, false
	}
	consumed = i + 1 - start
	setup = ciSetup{Reference: fallbackSetupName, Pinned: true, fallback: true, before: prefix, Inputs: map[string]string{"api_key": "$DD_API_KEY", "languages": snippet.language}}
	for _, assign := range declaration.Args {
		if assign.Name == nil || assign.Value == nil {
			continue
		}
		input := ""
		switch name := assign.Name.Value; {
		case name == "DD_SITE":
			input = "site"
		case strings.HasPrefix(name, "DD_TRACER_VERSION_"):
			input = snippet.language + "-tracer-version"
		default:
			continue
		}
		value, _ := ciShellWord(assign.Value, known, "", "")
		setup.Inputs[input] = gitLabLiteralInput(value, !ciShellExpands(assign.Value))
	}
	after := clause.Then[first+2:]
	for _, stmt := range after {
		snippet.after = append(snippet.after, shellText(stmt))
	}
	shaped := !slices.ContainsFunc(after, func(stmt *syntax.Stmt) bool { return !gitLabStateOnly(stmt) })
	if _, known := fallbackLanguage(snippet.language); !known {
		shaped = false
	}
	if shaped {
		expected, parsed := parseCIShell(snippet.shell())
		shaped = parsed && len(expected.Stmts) == consumed
		for j := 0; shaped && j < consumed; j++ {
			shaped = shellText(expected.Stmts[j]) == shellText(stmts[start+j])
		}
	}
	if !shaped {
		setup.Problem = "The step runs a " + fallbackSetupName + " in a form ddtest does not recognize; give it exactly the form ddtest's instructions show, on its own, with the step's other commands before or after it."
		return setup, consumed, true
	}
	setup.shaped, setup.Supported = true, true
	setup.after = after
	if !fallbackCommented(text, stmts, start) {
		setup.Problem = "The " + fallbackSetupName + " lacks the comment ddtest's instructions put right above it, which tells readers why Datadog's official installer is not used and recommends adding Bash and curl to the image; add these lines above it:\n" + strings.Join(fallbackComment, "\n")
	}
	return setup, consumed, true
}

// fallbackCommented reports whether the comment lines stand right above
// stmts[start] in text, the text stmts were parsed from: the last lines
// between the statement before it and it.
func fallbackCommented(text string, stmts []*syntax.Stmt, start int) bool {
	from, to := 0, int(stmts[start].Pos().Offset())
	if start > 0 {
		from = int(stmts[start-1].End().Offset())
	}
	if to > len(text) || from > to {
		return false
	}
	var lines []string
	for _, line := range strings.Split(text[from:to], "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return len(lines) >= len(fallbackComment) && slices.Equal(lines[len(lines)-len(fallbackComment):], fallbackComment)
}

// fallbackSetupProblem is setupProblem for the fallback setup, whichever
// provider's jobs run it: ddtest wrote it, so its shape is checked when it
// is read; what remains is its release and site.
func fallbackSetupProblem(setup ciSetup, tracerInput string, exact func(string) bool, example, site string) string {
	if setup.Problem != "" {
		return setup.Problem
	}
	if tracerInput != "" {
		if tracer := setup.Inputs[tracerInput]; !exact(tracer) {
			return "The " + fallbackSetupName + "'s " + fallbackSetupInput(tracerInput) + " " + fmt.Sprintf("%q", tracer) + " is not an exact release; set an exact version such as " + example + "."
		}
	}
	configured := setup.Inputs["site"]
	if configured == "" {
		configured = defaultDatadogSite
	}
	if site != "" && configured != site {
		return "The " + fallbackSetupName + " reports to " + configured + ", but the selected Datadog site is " + site + "; set DD_SITE=\"" + site + "\" in it."
	}
	return ""
}

// fallbackSetupInput names a setup input the way the fallback setup spells
// it: js-tracer-version is DD_TRACER_VERSION_JS.
func fallbackSetupInput(name string) string {
	if language, ok := strings.CutSuffix(name, "-tracer-version"); ok {
		return fallbackVersionVariable(language)
	}
	return buildkite{}.setupInput(name)
}

// imageFallback is the setup mechanism of a job whose image cannot run
// Datadog's official installer (see runtimeJob.SetupProvider): the fallback
// setup, in the shell that runs the tests, of the provider whose jobs it
// sets up. image is the job's image and missing what it lacks (see
// imageMissingTools).
type imageFallback struct {
	ciProvider
	image, missing string
}

// newImageFallback is the fallback mechanism for a job of host in image.
func newImageFallback(host ciProvider, image, missing string) imageFallback {
	return imageFallback{ciProvider: host, image: image, missing: missing}
}

// setupHost is the provider whose own setup mechanism a job's mechanism
// stands in for: the host of a fallback, or the mechanism itself.
func setupHost(provider ciProvider) ciProvider {
	if fallback, ok := provider.(imageFallback); ok {
		return fallback.ciProvider
	}
	return provider
}

func (imageFallback) setupName() string { return fallbackSetupName }

func (imageFallback) setupInput(name string) string { return fallbackSetupInput(name) }

func (f imageFallback) setupProblem(setup ciSetup, tracerInput string, exact func(string) bool, example, site string) string {
	if setup.fallback {
		return fallbackSetupProblem(setup, tracerInput, exact, example, site)
	}
	return f.ciProvider.setupProblem(setup, tracerInput, exact, example, site)
}

// form is the fallback setup's form in the host's jobs.
func (f imageFallback) form() snippetForm {
	switch f.ciProvider.(type) {
	case jenkinsContainer:
		return jenkinsSnippetForm
	case buildkite:
		return buildkiteSnippetForm
	case circleCI:
		return circleSnippetForm
	default:
		return gitLabSnippetForm
	}
}

// snippet is the fallback setup for request, with the statements a Jenkins
// container stage runs first where the Datadog plugin may reach it.
func (f imageFallback) snippet(request ciSetupRequest) fallbackSnippet {
	snippet := fallbackRequestSnippet(request, f.form())
	if container, ok := f.ciProvider.(jenkinsContainer); ok && container.reset {
		snippet.before = jenkinsPluginResets(request.Language)
	}
	return snippet
}

// circle reports a CircleCI host, whose steps each start a shell of their
// own: the fallback runs in the step that runs the tests.
func (f imageFallback) circle() bool { return f.name() == circleCIProvider }

func (f imageFallback) apiKeyAction(skipped bool) string {
	if _, ok := f.ciProvider.(jenkinsContainer); ok {
		return f.ciProvider.apiKeyAction(skipped)
	}
	return "Provide DD_API_KEY to the CI jobs that run the tests from " + f.ciProvider.setupTerms().SecretStore + ". Never write the key into the CI configuration. Until it is available, the " + fallbackSetupName + " in those jobs logs that Test Optimization is disabled and their tests run untraced."
}

// The fallback runs in the shell of the tests: in a CircleCI run step, its
// exports reach only the later commands of that step.
func (f imageFallback) setupTerms() ciSetupTerms {
	terms := f.ciProvider.setupTerms()
	terms.Installer = "setup"
	terms.SetupEntry = "the " + fallbackSetupName
	terms.Fallback = true
	terms.Entry = "the setup"
	if _, ok := f.ciProvider.(jenkinsContainer); ok {
		terms.Persist = func(command string) string { return command + " in the same sh script, after the setup" }
	}
	if f.circle() {
		terms.Later = "later commands of the same run step"
		terms.Persist = func(command string) string { return command + " in the same run step, after the setup" }
		terms.PersistEntry = func(command string) string { return command }
		terms.ImageGuard = func(string, string, string) string { return "" }
	}
	return terms
}

// recommendation is the user's action for the jobs of the fallback, for the
// language whose installer token is given.
func (f imageFallback) recommendation(token string) string {
	prescribed := "so ddtest prescribes its equivalent " + fallbackSetupName + " there"
	if reason := f.unavailable(token); reason != "" {
		prescribed = "and ddtest cannot set the job up without them either: " + reason
	}
	return "Datadog's official installation script cannot run in image " + f.image + ", which has " + f.missing + ", " + prescribed + ". ddtest does not change CI images, and the agent must not either: tell the user that " + fallbackUserRecommendation + "."
}

// unavailable explains why the fallback setup cannot run in the image for
// the language whose installer token is given; empty when it can.
func (f imageFallback) unavailable(token string) string {
	return fallbackUnavailable(f.image, token)
}

// fallbackUnavailable explains why the fallback setup cannot run in image
// for the language whose installer token is given; empty when it can.
func fallbackUnavailable(image, token string) string {
	language, known := fallbackLanguage(token)
	switch {
	case !known:
		return "ddtest has no setup without Bash or curl for this language"
	case imageWithoutShell(image):
		return "the image has no shell to run any setup in"
	}
	return language.ciFallbackUnavailable(image)
}

// checkFallbackPrerequisites gives every fallback setup a job of workflows
// already runs the problem of an image it cannot run in either (see
// fallbackUnavailable), as ddtest checks before prescribing it: in an older
// JDK's image, for one, it never installs the tracer and the tests run
// untraced. A job without an image ddtest reads keeps its runtime checks.
func checkFallbackPrerequisites(provider ciProvider, workflows []ciWorkflow) {
	for _, workflow := range workflows {
		for _, job := range workflow.Jobs {
			if job.Image == "" || job.ImageProblem != "" {
				continue
			}
			for i, step := range job.Steps {
				if step.Setup == nil || !step.Setup.fallback || step.Setup.Problem != "" {
					continue
				}
				reason := fallbackUnavailable(job.Image, step.Setup.Inputs["languages"])
				if reason == "" {
					continue
				}
				setup := *step.Setup
				setup.Problem = "The " + fallbackSetupName + " cannot run in image " + job.Image + ": " + reason + ", so it never installs the tracer and the tests run untraced."
				if _, fallback := fallbackJob(job); fallback {
					setup.Problem += " ddtest has no setup for this job: remove it, add no other Datadog setup, and do not change the image: tell the user that Datadog's official installation script cannot be used there because the image lacks Bash or curl, and that " + fallbackUserRecommendation + "."
				} else {
					setup.Problem += " Use the " + setupProviderFor(provider, job).setupName() + " instead, as ddtest's instructions show."
				}
				job.Steps[i].Setup = &setup
			}
		}
	}
}

// unavailableGap refuses to write the fallback setup for job, whose image
// cannot run it either; nil when it can.
func (f imageFallback) unavailableGap(location, token string) error {
	if reason := f.unavailable(token); reason != "" {
		return &SetupGap{location, f.why("The job's tests") + " ddtest has no equivalent setup for this job either: " + reason + ". Add no Datadog setup to it, and do not change its image: tell the user that Datadog's official installation script cannot be used there because the image lacks Bash or curl, and that " + fallbackUserRecommendation + "."}
	}
	return nil
}

// why says why the official installer cannot run in the jobs.
func (f imageFallback) why(jobs string) string {
	text := jobs + " run in " + f.image + ", which has " + f.missing + ", so Datadog's official installer cannot run there: it is a Bash script (" + buildkiteInstaller + ") downloaded with curl or wget"
	if f.circle() {
		text += ", and the Datadog orb also exports through $BASH_ENV, which only Bash reads"
	}
	return text + "."
}

func (f imageFallback) setupInstructions(request ciSetupRequest) string {
	jobs := "The jobs that run the tests"
	if len(request.Jobs) > 0 {
		jobs = "These jobs (" + strings.Join(request.Jobs, ", ") + ")"
	}
	tell := "Do not change their image or add Bash or curl to it yourself, and do not install them in the job: tell the user that Datadog's official installation script cannot be used in these jobs because their image lacks Bash or curl, and that " + fallbackUserRecommendation + "."
	var text strings.Builder
	if reason := f.unavailable(request.Language); reason != "" {
		text.WriteString("ddtest cannot set up Datadog Test Optimization in these jobs. " + f.why(jobs) + " ddtest has no equivalent setup for them either: " + reason + ". Add no Datadog setup to them. " + tell + " Until then, ddtest reports their tests as not set up.")
		return text.String()
	}
	language, _ := fallbackLanguage(request.Language)
	text.WriteString("Set up Datadog Test Optimization without Bash or curl. " + f.why(jobs) + " " + tell + " ddtest prescribes an equivalent setup for them in the meantime: POSIX sh that installs the same tracer release with the job's own " + language.ciFallbackTool() + " and exports the variables the official installer (" + buildkiteInstaller + ") exports, in the shell that runs the tests. Tell the user about it with the other CI changes.\n\n")
	text.WriteString("1. Ask the user to provide DD_API_KEY to those jobs: " + f.apiKeyAction(true) + "\n")
	snippet := f.snippet(request).shell()
	switch f.ciProvider.(type) {
	case jenkinsContainer:
		text.WriteString(jenkinsSnippetInstructions(request, snippet))
		text.WriteString("\nIt installs the tracers into a new folder of the workspace for each shell (" + jenkinsTracerFolder + "): add " + jenkinsTracerIgnore + " to the repository .gitignore, without a leading /. Keep those stages out of every datadog(…) option's scope, as for the setup snippet.\n")
	case buildkite:
		text.WriteString("2. In every Buildkite command step of those jobs that runs the tests, add this entry to its commands immediately before the first test command, after dependency installation and any cd into the test directory; in a command written as one block (command: |), add its lines inside the block, before the test command. Keep every existing command, env, plugin and condition unchanged, and don't add Datadog's setup snippet to these steps.\n\n```yaml\n")
		lines := []string{"- |"}
		for _, line := range strings.Split(buildkiteEscape(snippet), "\n") {
			lines = append(lines, "  "+line)
		}
		text.WriteString(strings.Join(lines, "\n") + "\n```\n")
		text.WriteString("\nWrite every $ in the pipeline as $$, as above. With the docker or docker-compose plugin, add DD_API_KEY to the plugin's environment list, so the key reaches the container.\n")
	default:
		if f.circle() {
			text.WriteString("2. In every run step of those jobs that runs the tests, add these lines at the start of the step's command, before the test command and after any cd into the test directory, writing a one-line command as a block (command: |). CircleCI starts every step in a new shell, and a step in an image without Bash never reads $BASH_ENV, so the setup runs in the same step as the tests; a job whose tests run in several steps needs it in each. Don't add the Datadog orb to these jobs, and keep every other step, parameter, environment and workflow unchanged:\n\n```yaml\n- run:\n    name: <the step's name>\n    command: |\n")
			for _, line := range strings.Split(snippet, "\n") {
				text.WriteString("      " + line + "\n")
			}
			text.WriteString("      <the test command>\n```\n")
		} else {
			text.WriteString("2. In every " + f.displayName() + " job of those that runs the tests, add this entry to its script immediately before the first test command, after dependency installation and any cd into the test directory. Keep every existing command, variable and rule unchanged, and don't add Datadog's setup script to these jobs.\n\n```yaml\n- |\n")
			for _, line := range strings.Split(snippet, "\n") {
				text.WriteString("  " + line + "\n")
			}
			text.WriteString("```\n")
		}
	}
	text.WriteString("\nKeep the two comment lines at its top: they tell readers why the official installer is not used, and ddtest checks for them. Replace nothing else in it but the release.\n")
	text.WriteString("\n3. Set the environment and working directory the tests depend on before the setup. After it, extend the variables it exports instead of replacing them.\n")
	if f.circle() {
		text.WriteString(circleSharedSetupInstructions(request, "4", snippet, true))
	}
	for _, note := range request.Notes {
		text.WriteString("\n- " + strings.ReplaceAll(note, "\n", "\n  "))
	}
	return strings.TrimSpace(text.String())
}

// fallbackInstead tells to replace Datadog's official setup in subject,
// whose image cannot run it, with the fallback setup.
func fallbackInstead(subject string) string {
	return "Use the " + fallbackSetupName + " in " + subject + " instead, as ddtest's instructions show, and do not change the image: tell the user that Datadog's official installation script cannot be used there because the image lacks Bash or curl, and that " + fallbackUserRecommendation + "."
}

// fallbackJobs are the validated CI jobs of check that run the setup without
// Bash or curl, as workflow / job.
func fallbackJobs(check *RuntimeCheck) []string {
	if check == nil {
		return nil
	}
	var jobs []string
	for _, finding := range check.Jobs {
		if location := finding.Workflow + " / " + finding.Job; finding.Action == fallbackSetupName && !slices.Contains(jobs, location) {
			jobs = append(jobs, location)
		}
	}
	return jobs
}

// fallbackCaveat is what the final report says about the jobs that run the
// setup without Bash or curl, for every language: what it gives up.
func fallbackCaveat(check *RuntimeCheck) []string {
	jobs := fallbackJobs(check)
	if len(jobs) == 0 {
		return nil
	}
	return []string{"The image of " + strings.Join(jobs, ", ") + " lacks the Bash or curl Datadog's installer needs, so ddtest's " + fallbackSetupName + " installs the tracer there in its place. " + fallbackSimpler}
}

// fallbackSimpler is why the user would rather have Datadog's installer.
const fallbackSimpler = "With a Bash-capable image, the installer would set Test Optimization up instead, which is simpler and more reliable: it is Datadog's documented setup, and it keeps up with the installer's changes; it also needs curl or wget there."

// FallbackHumanActions are the user's actions for the validated CI jobs
// that run the setup without Bash or curl: adding Bash and curl to their
// image, which neither ddtest nor the agent does.
func FallbackHumanActions(check *RuntimeCheck) []string {
	jobs := fallbackJobs(check)
	if len(jobs) == 0 {
		return nil
	}
	return []string{"Recommended: add Bash and curl to the image of " + strings.Join(jobs, ", ") + ", then switch those jobs to Datadog's official installer; Datadog's official installation script cannot run in that image today, so they use ddtest's equivalent " + fallbackSetupName + ". Only the user changes the image: ddtest and the agent never do."}
}

// fallbackJob reports a job whose setup mechanism is the fallback setup.
func fallbackJob(job runtimeJob) (imageFallback, bool) {
	fallback, ok := job.SetupProvider.(imageFallback)
	return fallback, ok
}
