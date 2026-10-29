// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
	"mvdan.cc/sh/v3/syntax"
)

// circleSetupOrb is Datadog's Test Optimization orb, and circleSetupAlias
// the alias its README declares it under. Its autoinstrument command runs
// the same installer as the other setup mechanisms and appends what it
// exports to $BASH_ENV, so every later step of the job sees it.
const (
	circleSetupOrb   = "datadog/test-optimization-circleci-orb"
	circleSetupAlias = "test-optimization-circleci-orb"
	// circleSetupReference is the reference ddtest prescribes: the moving
	// major, which receives reviewed v2 releases.
	circleSetupReference = circleSetupOrb + "@2"
)

// circleSetupVersion is an orb reference ddtest validates: the moving v2
// major or an exact v2 release. v1, dev: and volatile references are not.
var circleSetupVersion = regexp.MustCompile(`^2(?:\.[0-9]+\.[0-9]+)?$`)

// circleSetup reads the orb step's parameters as setup inputs: languages,
// site, service and <language>_tracer_version as <language>-tracer-version.
// A parameter ddtest cannot resolve stays unresolved.
func (c *circleConfig) circleSetup(orb circleOrb, arguments map[string]*yaml.Node, scope *circleScope) ciSetup {
	setup := ciSetup{Reference: orb.reference, Pinned: orb.version != "", Supported: circleSetupVersion.MatchString(orb.version), shaped: true, Inputs: map[string]string{}}
	for key, value := range arguments {
		text := c.substitute(gitLabScalar(value), scope)
		if circleReads(text) || gitLabAlias(value).Kind != yaml.ScalarNode {
			text = unresolvedCIValue(text)
		}
		input := key
		if language, ok := strings.CutSuffix(key, "_tracer_version"); ok && language != "" {
			input = language + "-tracer-version"
		}
		setup.Inputs[input] = text
	}
	return setup
}

// circleSetupProblem explains why the orb cannot set up the job it runs in:
// its exports reach later steps only through Bash, and it downloads the
// installer with curl or wget.
func circleSetupProblem(job *circleJob, conditional string, curl bool) string {
	switch {
	case job.legacy:
		return "The configuration declares version 2, where CircleCI rejects orbs; set version: 2.1, which reads version 2 configurations the same way."
	case job.keyProblem:
		return "The job's configuration sets DD_API_KEY in an environment mapping; never commit the key, and CircleCI sets such values as written: remove it and store DD_API_KEY in the CircleCI project settings or a context."
	case conditional != "":
		return "The Datadog orb step runs only under a condition ddtest cannot decide locally (" + conditional + "); run it unconditionally before the tests."
	case job.imageProblem != "":
		return "The job's executor cannot be checked: " + job.imageProblem + ", so ddtest cannot tell whether its image has the Bash and the curl or wget the Datadog orb needs. Give the job a docker image written in the configuration that has them, such as a cimg/ convenience image."
	case job.shell != "" && shellProgram(job.shell) != "bash":
		return "The job runs its steps with " + job.shell + ", but the Datadog orb's step needs Bash and only Bash reads the $BASH_ENV it exports through. Run the job with the default shell or /bin/bash."
	case imageWithoutBash(job.image):
		return "The job image " + job.image + " has no Bash, so CircleCI runs its steps with /bin/sh: the Datadog orb's step cannot run and $BASH_ENV, which carries its exports, is never read. Use a Bash-capable image for this job, such as a cimg/ convenience image or the non-Alpine variant."
	case !curl && imageWithoutDownloader(job.image):
		return "The job image " + job.image + " has neither curl nor wget, which the Datadog orb needs to download the installer, so the job would fail. Use the full image (without -slim) or a cimg/ convenience image, or install curl in an earlier step: apt-get update && apt-get install -y --no-install-recommends curl."
	}
	return ""
}

// shellProgram is the program a shell setting runs, such as bash for
// /bin/bash -eo pipefail.
func shellProgram(shell string) string {
	fields := strings.Fields(shell)
	if len(fields) == 0 {
		return ""
	}
	return fields[0][strings.LastIndex(fields[0], "/")+1:]
}

// circleShellDialect is how what CircleCI steps append to $BASH_ENV reads.
var circleShellDialect = ciShellDialect{label: "CircleCI $BASH_ENV", projectVariable: "CIRCLE_WORKING_DIRECTORY", runnerPrefixes: []string{"CIRCLE_", "CI"}}

// circleBashEnv matches a $BASH_ENV redirection target.
var circleBashEnv = regexp.MustCompile(`^"?\$\{?BASH_ENV\}?"?$`)

// circleFollowBashEnv records what a run step appends to $BASH_ENV with
// echo, as in echo 'export NAME=value' >> "$BASH_ENV", into the state
// later steps start from. It reports whether the step does only that, so it
// runs nothing later steps must replay. Text ddtest cannot evaluate, or
// that runs commands in every later step, leaves the step as it is.
func circleFollowBashEnv(state *ciShellState, command string, known map[string]string) bool {
	if !strings.Contains(command, "BASH_ENV") {
		return false
	}
	file, ok := parseCIShell(command)
	if !ok || len(file.Stmts) == 0 {
		return false
	}
	var appended []string
	values := map[string]string{}
	for _, stmt := range file.Stmts {
		call, ok := stmt.Cmd.(*syntax.CallExpr)
		if !ok || stmt.Negated || stmt.Background || len(call.Assigns) > 0 || len(call.Args) < 2 || call.Args[0].Lit() != "echo" {
			return false
		}
		if len(stmt.Redirs) != 1 || stmt.Redirs[0].Op != syntax.AppOut || !circleBashEnv.MatchString(shellText(stmt.Redirs[0].Word)) {
			return false
		}
		var words []string
		for _, word := range call.Args[1:] {
			text, ok := circleEchoWord(word, mergeEnvironment(known, state.references), values)
			if !ok {
				return false
			}
			words = append(words, text)
		}
		appended = append(appended, strings.Join(words, " "))
	}
	for _, text := range appended {
		file, ok := parseCIShell(text)
		if !ok {
			return false
		}
		for _, stmt := range file.Stmts {
			if !circleStateOnly(stmt) {
				return false
			}
		}
	}
	// Every appended statement only changes state.
	for _, text := range appended {
		file, _ := parseCIShell(text)
		for _, stmt := range file.Stmts {
			state.statement(stmt, mergeEnvironment(mergeEnvironment(mergeEnvironment(known, state.env), state.references), values))
		}
	}
	return true
}

// circleStateOnly reports a statement that only exports or assigns
// variables, possibly behind a test on their values.
func circleStateOnly(stmt *syntax.Stmt) bool {
	if stmt.Background || stmt.Negated || stmt.Coprocess || len(stmt.Redirs) > 0 {
		return false
	}
	switch command := stmt.Cmd.(type) {
	case *syntax.DeclClause:
		return command.Variant.Value == "export"
	case *syntax.CallExpr:
		return len(command.Args) == 0
	case *syntax.IfClause:
		for clause := command; clause != nil; clause = clause.Else {
			for _, inner := range clause.Then {
				if !circleStateOnly(inner) {
					return false
				}
			}
		}
		return true
	}
	return false
}

// circleEchoWord is the text echo writes for one word. A variable it
// expands is written as a reference to a placeholder holding its value, so
// the appended text parses as the shell text it is; values receives the
// placeholders. Unknown variables stay references, which later steps
// expand.
func circleEchoWord(word *syntax.Word, known, values map[string]string) (string, bool) {
	var text strings.Builder
	var parts func([]syntax.WordPart, bool) bool
	parts = func(items []syntax.WordPart, quoted bool) bool {
		for _, part := range items {
			switch part := part.(type) {
			case *syntax.Lit:
				value := part.Value
				if quoted {
					value = strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\$`, `$`, "\\`", "`").Replace(value)
				} else {
					value = strings.ReplaceAll(value, `\`, "")
				}
				text.WriteString(value)
			case *syntax.SglQuoted:
				text.WriteString(part.Value)
			case *syntax.DblQuoted:
				if !parts(part.Parts, true) {
					return false
				}
			case *syntax.ParamExp:
				if part.Index != nil || part.Slice != nil || part.Repl != nil || !ciEmptyDefault(part.Exp) || part.Excl || part.Length {
					return false
				}
				name := part.Param.Value
				value, ok := known[name]
				if !ok {
					text.WriteString("${" + name + "}")
					continue
				}
				placeholder := "__ddtest_echo_" + strconv.Itoa(len(values))
				values[placeholder] = value
				text.WriteString("${" + placeholder + "}")
			default:
				return false
			}
		}
		return true
	}
	if !parts(word.Parts, false) {
		return "", false
	}
	return text.String(), true
}

// setupInput names an orb parameter: js-tracer-version is
// js_tracer_version.
func (circleCI) setupInput(name string) string {
	return strings.ReplaceAll(name, "-", "_")
}

func (circleCI) setupName() string { return "Datadog Test Optimization orb" }

func (c circleCI) setupProblem(setup ciSetup, tracerInput string, exact func(string) bool, example, site string) string {
	if setup.Problem != "" {
		return setup.Problem
	}
	if !setup.Supported {
		return "The Datadog orb " + setup.Reference + " is not a supported release; declare " + circleSetupReference + " (or an exact 2.x.y release)."
	}
	tracer, set := setup.Inputs[tracerInput]
	if !set || strings.TrimSpace(tracer) == "" {
		return "The Datadog orb step sets no " + c.setupInput(tracerInput) + ", so it installs the default of whichever orb release runs; set an exact version such as " + example + "."
	}
	if !exact(tracer) {
		return "The Datadog orb step's " + c.setupInput(tracerInput) + " " + fmt.Sprintf("%q", tracer) + " is not an exact release; set an exact version such as " + example + "."
	}
	configured := setup.Inputs["site"]
	if configured == "" {
		configured = defaultDatadogSite
	}
	if site != "" && configured != site {
		return "The Datadog orb step reports to " + configured + ", but the selected Datadog site is " + site + "; set site: " + site + " on the step."
	}
	return ""
}

// circleMissingKey explains what happens to a job without DD_API_KEY: the
// orb's step fails, and with it the job.
const circleMissingKey = "Without it the Datadog orb step fails, which fails the job: that includes builds that don't receive the project's environment variables, such as forked pull requests unless \"Pass secrets to builds from forked pull requests\" is enabled, and jobs a restricted context does not reach (other branches or security groups)."

func (circleCI) apiKeyAction(bool) string {
	return "Add DD_API_KEY as an environment variable in the CircleCI project settings (Project Settings > Environment Variables), or in a context every test job uses. " + circleMissingKey
}

// circleQuote single-quotes text for the shell.
func circleQuote(text string) string {
	return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
}

// circlePersist is a run step that appends command to $BASH_ENV, so every
// later step of the job runs it first.
func circlePersist(command string) string {
	return "echo " + circleQuote(command) + ` >> "$BASH_ENV"`
}

// The orb's step runs in its own shell and exports through $BASH_ENV.
func (circleCI) setupTerms() ciSetupTerms {
	return ciSetupTerms{
		Installer:  "orb",
		Entry:      "the orb step",
		SetupEntry: "the Datadog orb step",
		Later:      "later steps of the job",
		Variables:  "the job's environment",
		Persist: func(command string) string {
			return "a later step that appends it to $BASH_ENV, such as " + circlePersist(command)
		},
		PersistEntry: func(command string) string {
			return "- run: " + strconvQuoteYAML(circlePersist(command))
		},
		MatrixExclusion: "with the workflow matrix's exclude",
		SharedMatrix:    "a workflow matrix shares the job's steps",
		ImageGuard: func(template, image, command string) string {
			if !circleReads(template) {
				return ""
			}
			return "- when:\n    condition:\n      equal: [ " + strconv.Quote(image) + ", " + strconv.Quote(template) + " ]\n    steps:\n      - run: " + strconvQuoteYAML(command)
		},
	}
}

// strconvQuoteYAML writes text as a YAML scalar: plain when it reads back
// as itself, otherwise double-quoted.
func strconvQuoteYAML(text string) string {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte("value: "+text), &node); err == nil && len(node.Content) == 1 {
		value := gitLabMembers(node.Content[0])["value"]
		if value != nil && value.Kind == yaml.ScalarNode && value.Tag == "!!str" && value.Value == text && !strings.ContainsAny(text, "#\n") {
			return text
		}
	}
	return strconv.Quote(text)
}

// circleSetupStep is the orb step for request, as list entries indented by
// indent with parameters nested nested columns deeper than the step's name,
// under alias: the autoinstrument step, then a step appending the
// commands that must follow it to $BASH_ENV.
func circleSetupStep(request ciSetupRequest, alias, indent string, nested int) string {
	child := "  " + strings.Repeat(" ", max(nested, 1))
	lines := []string{"- " + alias + "/autoinstrument:", child + "languages: " + request.Language, child + "site: " + request.Site}
	if request.TracerInput != "" {
		lines = append(lines, child+(circleCI{}).setupInput(request.TracerInput)+": "+strconvQuoteYAML(request.TracerVersion))
	}
	if len(request.After) > 0 {
		lines = append(lines, "- run:", child+"name: Configure later steps for Datadog Test Optimization", child+"command: |")
		for _, command := range request.After {
			lines = append(lines, child+"  "+circlePersist(command))
		}
	}
	return indent + strings.Join(lines, "\n"+indent) + "\n"
}

func (c circleCI) setupInstructions(request ciSetupRequest) string {
	var text strings.Builder
	text.WriteString("Set up Datadog Test Optimization in CircleCI with Datadog's Test Optimization orb (" + circleSetupReference + "), whose autoinstrument step installs the tracer and exports its configuration to the later steps of the job through $BASH_ENV:\n\n")
	text.WriteString("1. Ask the user to add DD_API_KEY as an environment variable in the CircleCI project settings (Project Settings > Environment Variables), or in a context every test job uses. Never write the key into the repository or the configuration. " + circleMissingKey + "\n")
	text.WriteString("2. Declare the orb in .circleci/config.yml, keeping the orbs already declared (an existing declaration of " + circleSetupOrb + " must use @2 or an exact 2.x.y release). CircleCI accepts orbs only in version 2.1: change version: 2 to version: 2.1, which reads the rest of a version 2 configuration the same way:\n\n```yaml\norbs:\n  " + circleSetupAlias + ": " + circleSetupReference + "\n```\n\n")
	text.WriteString("3. In every CircleCI job that runs the tests listed above, add this step immediately before the first test step, after checkout, dependency installation and any step that prepares the tests. Keep every existing step, parameter, environment and workflow unchanged; where a reusable command supplies the test step, add it there only when every job using that command runs these tests.\n\n```yaml\n")
	text.WriteString(circleSetupStep(request, circleSetupAlias, "", 2))
	text.WriteString("```\n")
	text.WriteString("\nThe step needs Bash and curl or wget in the job's image: CircleCI runs steps with /bin/sh in images without Bash (Alpine variants, BusyBox, distroless), where $BASH_ENV is never read, and official -slim images have neither curl nor wget. Use a cimg/ convenience image or another image with Bash and curl for those jobs.\n")
	text.WriteString("\n4. Set the environment and working directory the tests depend on before the step. After it, extend the variables it exports instead of replacing them: appended to $BASH_ENV, or inline before the test command; a later step's environment: does not apply to those variables, because $BASH_ENV overrides it.\n")
	for _, note := range request.Notes {
		text.WriteString("\n- " + strings.ReplaceAll(note, "\n", "\n  "))
	}
	return strings.TrimSpace(text.String())
}

// circleOrbAlias is the alias the Datadog orb is declared under in config,
// or the alias an edit declares it under; declared reports an existing
// declaration, supported whether it is a release ddtest validates.
func circleOrbAlias(config *circleConfig) (alias string, declared, supported bool) {
	for _, name := range slices.Sorted(maps.Keys(config.orbs)) {
		if orb := config.orbs[name]; orb.path == circleSetupOrb {
			return name, true, circleSetupVersion.MatchString(orb.version)
		}
	}
	alias = circleSetupAlias
	for {
		if _, taken := config.orbs[alias]; !taken {
			return alias, false, false
		}
		alias = "datadog-" + alias
	}
}
