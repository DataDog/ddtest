// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Buildkite has no Datadog plugin: the setup runs Datadog's installer, the
// one Datadog's GitLab script and CircleCI orb wrap, directly in the test
// step's commands. buildkiteInstaller pins its release and
// buildkiteInstallerChecksum its SHA256; nothing else names either.
const (
	buildkiteInstaller         = "install_test_visibility_v15.sh"
	buildkiteInstallerChecksum = "e64a4a45e0b171f4294b9f949f7fc99946192f952a2d463c61fad3cb8b956a58"
	buildkiteInstallerBase     = "https://install.datadoghq.com/scripts/"
	// The installer's download and the KEY=VALUE lines it prints, in the
	// .datadog folder it installs the tracers into.
	buildkiteInstallerFile   = ".datadog/install_test_visibility.sh"
	buildkiteInstallerOutput = ".datadog/test-optimization.env"
)

// snippetForm is what the setup snippet differs in between the providers
// whose test steps run it (see buildkiteSnippet): the CI provider the
// tracers' telemetry records for an automatic installation, as the
// wrappers set gitlab or circleci; where it installs the tracers; and how
// it tests for the key.
type snippetForm struct {
	provider string
	// folder, when set, is the DD_TRACER_FOLDER the snippet exports as
	// written, such as a mktemp -d in the workspace: the installer installs
	// the tracers there instead of its default .datadog, and the snippet
	// keeps its download and output there too.
	folder string
	// keySet tests the key with [ "${DD_API_KEY:+set}" = set ], which a
	// shell trace (sh -x) prints without the key's value; otherwise with
	// [ -n "${DD_API_KEY:-}" ].
	keySet bool
	// shellSetups reports a provider whose container steps run a
	// language's setup for a POSIX shell (see ciShellSetup) where the
	// image has no Bash.
	shellSetups bool
}

// ciSnippetFormer is a provider's setup mechanism that runs the setup
// snippet in form.
type ciSnippetFormer interface {
	snippetForm() snippetForm
}

// ciShellSetup is a language whose setup a POSIX shell runs alone, for a
// container whose image has no Bash, which Datadog's installer needs.
type ciShellSetup interface {
	// shellSetup is that setup, reporting to site with the exact tracer
	// release, in form; it starts with form's key guard, as the setup
	// snippet does.
	shellSetup(site, release string, form snippetForm) string
	// shellSetupAt recognizes it starting at stmts[i], as snippetSetupAt
	// recognizes the setup snippet; the setup is withoutBash.
	shellSetupAt(stmts []*syntax.Stmt, i int, form snippetForm) (setup ciSetup, consumed int, ok bool)
}

// buildkiteSnippetForm is the snippet Buildkite steps run.
var buildkiteSnippetForm = snippetForm{provider: "buildkite"}

// files are the installer's download and output paths as the snippet's
// commands write them, and inside a double-quoted string.
func (f snippetForm) files() (installer, output, quotedInstaller string) {
	if f.folder == "" {
		return buildkiteInstallerFile, buildkiteInstallerOutput, buildkiteInstallerFile
	}
	return `"$DD_TRACER_FOLDER/install_test_visibility.sh"`, `"$DD_TRACER_FOLDER/test-optimization.env"`, "$DD_TRACER_FOLDER/install_test_visibility.sh"
}

// checksumPattern matches the checksum line of the form's snippet.
func (f snippetForm) checksumPattern() *regexp.Regexp {
	_, _, file := f.files()
	return regexp.MustCompile(`echo "([0-9a-f]*)  ` + regexp.QuoteMeta(file) + `"`)
}

// keyGuard is the condition of the snippet's guard.
func (f snippetForm) keyGuard() string {
	if f.keySet {
		return `[ "${DD_API_KEY:+set}" = set ]`
	}
	return `[ -n "${DD_API_KEY:-}" ]`
}

// buildkiteMissingKeyMessage is what a step without DD_API_KEY logs: the
// tests still run, untraced.
const buildkiteMissingKeyMessage = "DD_API_KEY is not set; Datadog Test Optimization is disabled for this step."

// buildkiteFailedInstallMessage is what a step logs when the installer
// cannot be downloaded, verified or run: the tests still run, untraced.
const buildkiteFailedInstallMessage = "Datadog Test Optimization could not be installed; the tests run without it."

var (
	buildkiteBuildSystem       = regexp.MustCompile(`^DD_INSTRUMENTATION_BUILD_SYSTEM_([A-Z]+)$`)
	buildkiteInstallerURL      = regexp.MustCompile(`https://install\.datadoghq\.com/scripts/(install_test_visibility_v[0-9]+\.sh)`)
	buildkiteTracerVariable    = regexp.MustCompile(`^DD_SET_TRACER_VERSION_([A-Z]+)$`)
	buildkiteInstallerMentions = regexp.MustCompile(`install\.datadoghq\.com/scripts/install_test_visibility`)
)

// buildkiteSetupInput maps the installer's variables to the setup action's
// input names; DD_SET_TRACER_VERSION_X becomes x-tracer-version, and
// DD_INSTRUMENTATION_BUILD_SYSTEM_X x-instrumented-build-system.
func buildkiteSetupInput(variable string) (string, bool) {
	switch variable {
	case "DD_CIVISIBILITY_INSTRUMENTATION_LANGUAGES":
		return "languages", true
	case "DD_SITE":
		return "site", true
	}
	if match := buildkiteTracerVariable.FindStringSubmatch(variable); match != nil {
		return strings.ToLower(match[1]) + "-tracer-version", true
	}
	if match := buildkiteBuildSystem.FindStringSubmatch(variable); match != nil {
		return strings.ToLower(match[1]) + "-instrumented-build-system", true
	}
	return "", false
}

func buildkiteSetupVariable(name string) bool {
	_, input := buildkiteSetupInput(name)
	return input
}

// setupInput names an installer input: js-tracer-version is
// DD_SET_TRACER_VERSION_JS.
func (buildkite) setupInput(name string) string {
	switch name {
	case "languages":
		return "DD_CIVISIBILITY_INSTRUMENTATION_LANGUAGES"
	case "site":
		return "DD_SITE"
	}
	if language, ok := strings.CutSuffix(name, "-tracer-version"); ok {
		return "DD_SET_TRACER_VERSION_" + strings.ToUpper(language)
	}
	if language, ok := strings.CutSuffix(name, "-instrumented-build-system"); ok {
		return "DD_INSTRUMENTATION_BUILD_SYSTEM_" + strings.ToUpper(language)
	}
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

func (buildkite) setupName() string { return "Datadog setup snippet" }

// buildkiteSnippet is the setup snippet's variable parts, as shell words.
type buildkiteSnippet struct {
	form    snippetForm
	guarded bool
	// site and languages are words as written, such as "datadoghq.com".
	site, languages string
	// tracers are the DD_SET_TRACER_VERSION_* assignments, in order.
	tracers []string
	// installer and checksum select the installer release.
	installer, checksum string
	// after are statements that run only with the setup, inside its guard.
	after []string
	// before are statements that run before it, whether or not it runs.
	before []string
}

// shell writes the snippet as shell text, with a literal $ (pipeline
// files escape it, see buildkiteSetupText). It installs nothing and exits
// nothing when the key is missing or the install fails: the step's commands
// run under -e, so every command that may fail is a condition.
func (s buildkiteSnippet) shell() string {
	url := buildkiteInstallerBase + s.installer
	install := append([]string{"DD_CIVISIBILITY_INSTRUMENTATION_LANGUAGES=" + s.languages}, s.tracers...)
	file, output, quoted := s.form.files()
	exports, folder := "export ", ".datadog"
	if s.form.folder != "" {
		exports, folder = `export DD_TRACER_FOLDER="`+s.form.folder+`" `, `"$DD_TRACER_FOLDER"`
	}
	core := []string{
		exports + "DD_SITE=" + s.site + ` DD_CIVISIBILITY_AUTO_INSTRUMENTATION_PROVIDER="` + s.form.provider + `"`,
		"if mkdir -p " + folder + " &&",
		"  { curl -fsSL -o " + file + " " + url + " || wget -qO " + file + " " + url + "; } &&",
		`  echo "` + s.checksum + "  " + quoted + `" | { sha256sum -c - || shasum -a 256 -c -; } >/dev/null 2>&1 &&`,
		"  " + strings.Join(install, " ") + " bash " + file + " >" + output + "; then",
		"  while IFS='=' read -r dd_name dd_value; do",
		`    case "$dd_name" in '' | [0-9]* | *[!A-Za-z0-9_]*) ;; *) export "$dd_name=$dd_value" ;; esac`,
		"  done <" + output,
		"else",
		`  echo "` + buildkiteFailedInstallMessage + `" >&2`,
		"fi",
	}
	if !s.guarded {
		return strings.Join(append(slices.Clone(s.before), core...), "\n")
	}
	lines := append(slices.Clone(s.before), "if "+s.form.keyGuard()+"; then")
	for _, line := range append(core, s.after...) {
		lines = append(lines, "  "+line)
	}
	lines = append(lines, "else", `  echo "`+buildkiteMissingKeyMessage+`" >&2`, "fi")
	return strings.Join(lines, "\n")
}

// buildkiteRequestSnippet is the snippet ddtest prescribes for request.
func buildkiteRequestSnippet(request ciSetupRequest) buildkiteSnippet {
	return requestSnippet(request, buildkiteSnippetForm)
}

// requestSnippet is the snippet in form ddtest prescribes for request.
func requestSnippet(request ciSetupRequest, form snippetForm) buildkiteSnippet {
	snippet := buildkiteSnippet{form: form, guarded: true, site: `"` + request.Site + `"`, languages: `"` + request.Language + `"`, installer: buildkiteInstaller, checksum: buildkiteInstallerChecksum, after: request.After}
	if request.TracerInput != "" {
		snippet.tracers = []string{(buildkite{}).setupInput(request.TracerInput) + `="` + request.TracerVersion + `"`}
	}
	return snippet
}

// buildkiteSetupAt recognizes the setup snippet starting at stmts[i]: the
// guarded form (one if statement testing DD_API_KEY) or the snippet's two
// statements without the guard, in exactly the shape ddtest writes, with
// any inputs, installer release and checksum. consumed is how many
// statements it spans. A statement that runs the installer in another shape
// is a setup with a problem, so the step's tests report it.
func buildkiteSetupAt(stmts []*syntax.Stmt, i int, known map[string]string) (setup ciSetup, consumed int, ok bool) {
	return snippetSetupAt(stmts, i, known, buildkiteSnippetForm, nil)
}

// snippetSetupAt recognizes the setup snippet in form starting at stmts[i],
// as buildkiteSetupAt does. When it starts with statements of before, which
// may run before the snippet, they are part of it: setup.before lists them.
func snippetSetupAt(stmts []*syntax.Stmt, i int, known map[string]string, form snippetForm, before []string) (setup ciSetup, consumed int, ok bool) {
	var prefix []string
	for start := i; i < len(stmts) && len(before) > 0; i++ {
		text := shellText(stmts[i])
		if !slices.ContainsFunc(before, func(statement string) bool {
			expected, parsed := parseCIShell(statement)
			return parsed && len(expected.Stmts) == 1 && shellText(expected.Stmts[0]) == text
		}) {
			break
		}
		prefix = append(prefix, text)
		consumed = i + 1 - start
	}
	if i >= len(stmts) {
		return ciSetup{}, 0, false
	}
	mentions := func(stmt *syntax.Stmt) bool { return buildkiteInstallerMentions.MatchString(shellText(stmt)) }
	_, export := stmts[i].Cmd.(*syntax.DeclClause)
	unguarded := export && strings.Contains(shellText(stmts[i]), "DD_CIVISIBILITY_AUTO_INSTRUMENTATION_PROVIDER") && i+1 < len(stmts) && mentions(stmts[i+1])
	if !mentions(stmts[i]) && !unguarded {
		if !form.shellSetups {
			return ciSetup{}, 0, false
		}
		for _, language := range discoveryLanguages {
			if shell, ok := language.(ciShellSetup); ok {
				if setup, spanned, ok := shell.shellSetupAt(stmts, i, form); ok {
					setup.before = prefix
					return setup, consumed + spanned, true
				}
			}
		}
		return ciSetup{}, 0, false
	}
	setup = ciSetup{Reference: "Datadog installer", Pinned: true, DefaultLatest: true, Inputs: map[string]string{"api_key": "$DD_API_KEY"}, before: prefix}
	var core, after []*syntax.Stmt
	guarded := false
	if clause, isIf := stmts[i].Cmd.(*syntax.IfClause); isIf && (gitLabKeyGuard(clause.Cond) == "DD_API_KEY" || keySetGuard(clause.Cond)) && clause.Else != nil && len(clause.Else.Cond) == 0 && len(clause.Then) >= 2 {
		core, after, guarded = clause.Then[:2], clause.Then[2:], true
		consumed++
	} else if unguarded {
		core = stmts[i : i+2]
		consumed += 2
	} else {
		core = stmts[i : i+1]
		consumed++
	}
	snippet := buildkiteSnippet{form: form, guarded: guarded, before: prefix}
	for _, stmt := range core {
		syntax.Walk(stmt, func(node syntax.Node) bool {
			var assigns []*syntax.Assign
			switch node := node.(type) {
			case *syntax.DeclClause:
				assigns = append(assigns, node.Args...)
			case *syntax.CallExpr:
				assigns = node.Assigns
			}
			for _, assign := range assigns {
				if assign.Name == nil || assign.Value == nil {
					continue
				}
				written := shellText(assign.Value)
				switch name := assign.Name.Value; {
				case name == "DD_SITE":
					snippet.site = written
				case name == "DD_CIVISIBILITY_INSTRUMENTATION_LANGUAGES":
					snippet.languages = written
				case buildkiteTracerVariable.MatchString(name):
					snippet.tracers = append(snippet.tracers, name+"="+written)
				case buildkiteBuildSystem.MatchString(name):
				default:
					continue
				}
				input, _ := buildkiteSetupInput(assign.Name.Value)
				value, _ := ciShellWord(assign.Value, known, "", buildkiteShellDialect.projectVariable)
				setup.Inputs[input] = gitLabLiteralInput(value, !ciShellExpands(assign.Value))
			}
			return true
		})
	}
	// The installer reads a build system setting the step exported before
	// it, as export DD_INSTRUMENTATION_BUILD_SYSTEM_JAVA=all, too.
	for name, value := range known {
		if input, ok := buildkiteSetupInput(name); ok && buildkiteBuildSystem.MatchString(name) {
			if _, set := setup.Inputs[input]; !set {
				setup.Inputs[input] = value
			}
		}
	}
	text := shellText(core[len(core)-1])
	if match := buildkiteInstallerURL.FindStringSubmatch(text); match != nil {
		snippet.installer = match[1]
		setup.Reference = "Datadog installer " + match[1]
	}
	if match := form.checksumPattern().FindStringSubmatch(text); match != nil {
		snippet.checksum = match[1]
	}
	for _, stmt := range after {
		snippet.after = append(snippet.after, shellText(stmt))
		if !gitLabStateOnly(stmt) {
			snippet.installer = "" // It runs a command, which would disappear with the setup.
		}
	}
	shaped := snippet.installer != "" && snippet.site != "" && snippet.languages != ""
	if shaped {
		start := i - len(prefix)
		expected, parsed := parseCIShell(snippet.shell())
		shaped = parsed && len(expected.Stmts) == consumed
		for j := 0; shaped && j < consumed; j++ {
			shaped = shellText(expected.Stmts[j]) == shellText(stmts[start+j])
		}
	}
	if !shaped {
		setup.Problem = "The step runs Datadog's installer in a form ddtest does not recognize; give it exactly the form of ddtest's setup snippet, on its own, with the step's other commands before or after it."
		return setup, consumed, true
	}
	setup.after = append(setup.after, after...)
	setup.shaped = true
	setup.Supported = snippet.installer == buildkiteInstaller && snippet.checksum == buildkiteInstallerChecksum
	return setup, consumed, true
}

// keySetGuard reports the condition [ "${DD_API_KEY:+set}" = set ] (see
// snippetForm.keySet).
func keySetGuard(condition []*syntax.Stmt) bool {
	expected, parsed := parseCIShell(snippetForm{keySet: true}.keyGuard())
	return parsed && len(condition) == 1 && len(expected.Stmts) == 1 && shellText(condition[0]) == shellText(expected.Stmts[0])
}

func (b buildkite) setupProblem(setup ciSetup, tracerInput string, exact func(string) bool, example, site string) string {
	return snippetSetupProblem(setup, tracerInput, exact, example, site)
}

// snippetSetupProblem is setupProblem for the setup snippet, whichever
// provider's steps run it.
func snippetSetupProblem(setup ciSetup, tracerInput string, exact func(string) bool, example, site string) string {
	if setup.Problem != "" {
		return setup.Problem
	}
	if !setup.Supported {
		return "The Datadog setup snippet runs " + setup.Reference + " without the pinned release and SHA256 ddtest validates; download " + buildkiteInstallerBase + buildkiteInstaller + " and check its SHA256 " + buildkiteInstallerChecksum + ", as ddtest's instructions show."
	}
	if tracer := setup.Inputs[tracerInput]; !exact(tracer) {
		return "The Datadog setup snippet's " + (buildkite{}).setupInput(tracerInput) + " " + fmt.Sprintf("%q", tracer) + " is not an exact release; set an exact version such as " + example + "."
	}
	configured := setup.Inputs["site"]
	if configured == "" {
		configured = defaultDatadogSite
	}
	if site != "" && configured != site {
		return "The Datadog setup snippet reports to " + configured + ", but the selected Datadog site is " + site + "; set DD_SITE=\"" + site + "\" in it."
	}
	return ""
}

// buildkiteKeySources are where Buildkite steps receive DD_API_KEY from.
const buildkiteKeySources = "an agent environment hook, a secrets plugin (such as the Buildkite secrets, AWS Secrets Manager or Vault plugins) or the pipeline's environment in the Buildkite UI"

func (buildkite) apiKeyAction(bool) string {
	return "Provide DD_API_KEY to the Buildkite steps that run the tests from " + buildkiteKeySources + "; with the docker or docker-compose plugin, the plugin's environment list passes it into the container. Never write the key into the pipeline YAML. Until it is available, those steps log that Test Optimization is disabled and their tests run untraced."
}

// buildkiteEscape writes shell text into a pipeline file, where pipeline
// upload would otherwise interpolate every $.
func buildkiteEscape(text string) string {
	return strings.ReplaceAll(text, "$", "$$")
}

// buildkiteSetupText is the setup snippet for request as a commands entry
// (a block scalar list item), every line prefixed with indent.
func buildkiteSetupText(request ciSetupRequest, indent string) string {
	lines := []string{"- |"}
	for _, line := range strings.Split(buildkiteEscape(buildkiteRequestSnippet(request).shell()), "\n") {
		lines = append(lines, "  "+line)
	}
	return indent + strings.Join(lines, "\n"+indent) + "\n"
}

// The snippet runs in the step's own shell: later commands of the step see
// its exports, other steps never do.
func (buildkite) setupTerms() ciSetupTerms {
	return ciSetupTerms{
		Installer:       "installer",
		SecretStore:     buildkiteKeySources + " (never the pipeline YAML; a docker or docker-compose plugin passes it into the container through its environment list)",
		Entry:           "the setup snippet",
		SetupEntry:      "the Datadog setup snippet",
		Later:           "later commands of the same step",
		Variables:       "the step's env",
		Persist:         buildkiteEscape,
		PersistEntry:    func(command string) string { return "- " + buildkiteEscape(command) },
		MatrixExclusion: "with matrix adjustments (skip: true on those combinations)",
		SharedMatrix:    "a matrix shares the step's commands",
		ImageGuard: func(template, image, command string) string {
			if !buildkiteMatrixReference.MatchString(template) {
				return ""
			}
			return `- if [ "` + template + `" = "` + image + `" ]; then ` + buildkiteEscape(command) + `; fi`
		},
	}
}

func (b buildkite) setupInstructions(request ciSetupRequest) string {
	var text strings.Builder
	text.WriteString("Set up Datadog Test Optimization in Buildkite with Datadog's installer (" + buildkiteInstaller + "), run in the test step's own commands: there is no Datadog Buildkite plugin, and every Buildkite step starts a fresh shell, possibly on another agent, so a separate setup step's exports would never reach the tests.\n\n")
	text.WriteString("1. Ask the user to provide DD_API_KEY to the Buildkite steps that run the tests from " + buildkiteKeySources + ". Never write the key into the repository or the pipeline YAML. Steps without the key skip the setup, log that Test Optimization is disabled, and run their tests untraced.\n")
	text.WriteString("2. In every Buildkite command step that runs the tests listed above, add this entry to its commands immediately before the first test command, after dependency installation and any cd into the test directory; in a command written as one block (command: |), add its lines inside the block, before the test command. Keep every existing command, env, plugin and condition unchanged; where a YAML anchor shares the commands, add it there only when every step using them runs these tests. Don't add it as a separate step.\n\n```yaml\n")
	text.WriteString(buildkiteSetupText(request, ""))
	text.WriteString("```\n")
	text.WriteString("\nWrite every $ in the pipeline as $$, as above: buildkite-agent pipeline upload replaces $NAME when the pipeline is uploaded, not when the step runs. The installer needs Bash and curl or wget where the step runs: on the agent host, or in the image of the step's docker or docker-compose plugin. Images without Bash (Alpine variants, BusyBox, distroless) or curl (official -slim images) need another image. With the docker or docker-compose plugin, add DD_API_KEY to the plugin's environment list, so the key reaches the container.\n")
	text.WriteString("\n3. Set the environment and working directory the tests depend on before the entry. After it, extend the variables it exports instead of replacing them.\n")
	for _, note := range request.Notes {
		text.WriteString("\n- " + strings.ReplaceAll(note, "\n", "\n  "))
	}
	return strings.TrimSpace(text.String())
}
