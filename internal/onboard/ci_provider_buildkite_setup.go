// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"regexp"
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
	// buildkiteInstallerProvider is the CI provider the tracers' telemetry
	// records for an automatic installation, as the wrappers set gitlab or
	// circleci.
	buildkiteInstallerProvider = "buildkite"
	// The installer's download and the KEY=VALUE lines it prints, in the
	// .datadog folder it installs the tracers into.
	buildkiteInstallerFile   = ".datadog/install_test_visibility.sh"
	buildkiteInstallerOutput = ".datadog/test-optimization.env"
)

// buildkiteMissingKeyMessage is what a step without DD_API_KEY logs: the
// tests still run, untraced.
const buildkiteMissingKeyMessage = "DD_API_KEY is not set; Datadog Test Optimization is disabled for this step."

// buildkiteFailedInstallMessage is what a step logs when the installer
// cannot be downloaded, verified or run: the tests still run, untraced.
const buildkiteFailedInstallMessage = "Datadog Test Optimization could not be installed; the tests run without it."

var (
	buildkiteInstallerURL      = regexp.MustCompile(`https://install\.datadoghq\.com/scripts/(install_test_visibility_v[0-9]+\.sh)`)
	buildkiteInstallerSum      = regexp.MustCompile(`echo "([0-9a-f]*)  ` + regexp.QuoteMeta(buildkiteInstallerFile) + `"`)
	buildkiteTracerVariable    = regexp.MustCompile(`^DD_SET_TRACER_VERSION_([A-Z]+)$`)
	buildkiteInstallerMentions = regexp.MustCompile(`install\.datadoghq\.com/scripts/install_test_visibility`)
)

// buildkiteSetupInput maps the installer's variables to the setup action's
// input names; DD_SET_TRACER_VERSION_X becomes x-tracer-version.
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
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

func (buildkite) setupName() string { return "Datadog setup snippet" }

// buildkiteSnippet is the setup snippet's variable parts, as shell words.
type buildkiteSnippet struct {
	guarded bool
	// site and languages are words as written, such as "datadoghq.com".
	site, languages string
	// tracers are the DD_SET_TRACER_VERSION_* assignments, in order.
	tracers []string
	// installer and checksum select the installer release.
	installer, checksum string
	// after are statements that run only with the setup, inside its guard.
	after []string
}

// shell writes the snippet as shell text, with a literal $ (pipeline
// files escape it, see buildkiteSetupText). It installs nothing and exits
// nothing when the key is missing or the install fails: the step's commands
// run under -e, so every command that may fail is a condition.
func (s buildkiteSnippet) shell() string {
	url := buildkiteInstallerBase + s.installer
	install := append([]string{"DD_CIVISIBILITY_INSTRUMENTATION_LANGUAGES=" + s.languages}, s.tracers...)
	core := []string{
		"export DD_SITE=" + s.site + ` DD_CIVISIBILITY_AUTO_INSTRUMENTATION_PROVIDER="` + buildkiteInstallerProvider + `"`,
		"if mkdir -p .datadog &&",
		"  { curl -fsSL -o " + buildkiteInstallerFile + " " + url + " || wget -qO " + buildkiteInstallerFile + " " + url + "; } &&",
		`  echo "` + s.checksum + "  " + buildkiteInstallerFile + `" | { sha256sum -c - || shasum -a 256 -c -; } >/dev/null 2>&1 &&`,
		"  " + strings.Join(install, " ") + " bash " + buildkiteInstallerFile + " >" + buildkiteInstallerOutput + "; then",
		"  while IFS='=' read -r dd_name dd_value; do",
		`    case "$dd_name" in '' | [0-9]* | *[!A-Za-z0-9_]*) ;; *) export "$dd_name=$dd_value" ;; esac`,
		"  done <" + buildkiteInstallerOutput,
		"else",
		`  echo "` + buildkiteFailedInstallMessage + `" >&2`,
		"fi",
	}
	if !s.guarded {
		return strings.Join(core, "\n")
	}
	lines := []string{`if [ -n "${DD_API_KEY:-}" ]; then`}
	for _, line := range append(core, s.after...) {
		lines = append(lines, "  "+line)
	}
	lines = append(lines, "else", `  echo "`+buildkiteMissingKeyMessage+`" >&2`, "fi")
	return strings.Join(lines, "\n")
}

// buildkiteRequestSnippet is the snippet ddtest prescribes for request.
func buildkiteRequestSnippet(request ciSetupRequest) buildkiteSnippet {
	snippet := buildkiteSnippet{guarded: true, site: `"` + request.Site + `"`, languages: `"` + request.Language + `"`, installer: buildkiteInstaller, checksum: buildkiteInstallerChecksum, after: request.After}
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
	mentions := func(stmt *syntax.Stmt) bool { return buildkiteInstallerMentions.MatchString(shellText(stmt)) }
	_, export := stmts[i].Cmd.(*syntax.DeclClause)
	unguarded := export && strings.Contains(shellText(stmts[i]), "DD_CIVISIBILITY_AUTO_INSTRUMENTATION_PROVIDER") && i+1 < len(stmts) && mentions(stmts[i+1])
	if !mentions(stmts[i]) && !unguarded {
		return ciSetup{}, 0, false
	}
	setup = ciSetup{Reference: "Datadog installer", Pinned: true, DefaultLatest: true, Inputs: map[string]string{"api_key": "$DD_API_KEY"}}
	var core, after []*syntax.Stmt
	guarded := false
	if clause, isIf := stmts[i].Cmd.(*syntax.IfClause); isIf && gitLabKeyGuard(clause.Cond) == "DD_API_KEY" && clause.Else != nil && len(clause.Else.Cond) == 0 && len(clause.Then) >= 2 {
		core, after, guarded, consumed = clause.Then[:2], clause.Then[2:], true, 1
	} else if unguarded {
		core, consumed = stmts[i:i+2], 2
	} else {
		core, consumed = stmts[i:i+1], 1
	}
	snippet := buildkiteSnippet{guarded: guarded}
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
	text := shellText(core[len(core)-1])
	if match := buildkiteInstallerURL.FindStringSubmatch(text); match != nil {
		snippet.installer = match[1]
		setup.Reference = "Datadog installer " + match[1]
	}
	if match := buildkiteInstallerSum.FindStringSubmatch(text); match != nil {
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
		expected, parsed := parseCIShell(snippet.shell())
		shaped = parsed && len(expected.Stmts) == consumed
		for j := 0; shaped && j < consumed; j++ {
			shaped = shellText(expected.Stmts[j]) == shellText(stmts[i+j])
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

func (b buildkite) setupProblem(setup ciSetup, tracerInput string, exact func(string) bool, example, site string) string {
	if setup.Problem != "" {
		return setup.Problem
	}
	if !setup.Supported {
		return "The Datadog setup snippet runs " + setup.Reference + " without the pinned release and SHA256 ddtest validates; download " + buildkiteInstallerBase + buildkiteInstaller + " and check its SHA256 " + buildkiteInstallerChecksum + ", as ddtest's instructions show."
	}
	if tracer := setup.Inputs[tracerInput]; !exact(tracer) {
		return "The Datadog setup snippet's " + b.setupInput(tracerInput) + " " + fmt.Sprintf("%q", tracer) + " is not an exact release; set an exact version such as " + example + "."
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
