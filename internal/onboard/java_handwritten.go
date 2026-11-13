// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bufio"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/gradle"
	"github.com/DataDog/ddtest/internal/platform"
	"mvdan.cc/sh/v3/syntax"
)

// A hand-written dd-java-agent setup attaches the agent to the build JVM,
// as dd-java-agent's documentation shows: -javaagent:<path>/dd-java-agent.jar
// in JAVA_TOOL_OPTIONS, MAVEN_OPTS for Maven, GRADLE_OPTS or
// org.gradle.jvmargs for Gradle, with a release the job downloads and the
// DD_* configuration in the job's environment or as -Ddd.* system
// properties. ddtest recognizes such a setup and lists what it lacks, but
// the Java CI check does not read it yet (see
// javaDiscovery.validatesHandWrittenSetup), so it is never certified.

// javaRecommendedTracer is the exact dd-java-agent release ddtest's setup
// names when it can't read the latest one from Maven Central (see
// javaSetupRelease): a release ddtest has validated.
const javaRecommendedTracer = "1.67.1"

// javaHandWrittenDownload is the download URL of the release ddtest selects
// (see javaSetupRelease), for messages.
func javaHandWrittenDownload() string {
	release := javaSetupRelease()
	return "https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/" + release + "/dd-java-agent-" + release + ".jar"
}

// javaHandWrittenCarrier is a JVM option list a hand-written setup attaches
// dd-java-agent through: its name, such as MAVEN_OPTS, and value.
type javaHandWrittenCarrier struct {
	name, value string
}

// javaHandWrittenSetup recognizes a hand-written dd-java-agent setup of the
// Maven or Gradle test command at index of job (see handWrittenSetup); nil
// when no JVM option list of the command attaches the agent.
func javaHandWrittenSetup(root string, provider ciProvider, workflow ciWorkflow, job runtimeJob, index int, row map[string]any, framework, site string) *handWrittenSetup {
	step := job.Steps[index]
	env := handWrittenEnvironment(workflow, job, step)
	directory := filepath.Clean(filepath.FromSlash(stepDirectory(workflow, job, step)))
	if strings.HasPrefix(directory, "..") || filepath.IsAbs(directory) || strings.Contains(directory, "$") {
		return nil
	}
	tool := javaStepBuildTool(resolveJavaStep(root, workflow, job, step, framework), step.Run)
	var carriers []javaHandWrittenCarrier
	commandEnv := env
	for _, key := range []string{"JAVA_TOOL_OPTIONS", "MAVEN_OPTS", "GRADLE_OPTS", "JAVA_OPTS"} {
		if value, set := env[key]; set {
			carriers = append(carriers, javaHandWrittenCarrier{key, value})
		}
	}
	if commands, err := staticCommands(step.Run); err == nil {
		for _, words := range commands {
			words, assignments := handWrittenCommandPrefix(words)
			if !mavenCommand(words) && !gradle.Command(words) {
				continue
			}
			commandEnv = mergeEnvironment(env, assignments)
			for _, key := range []string{"JAVA_TOOL_OPTIONS", "MAVEN_OPTS", "GRADLE_OPTS", "JAVA_OPTS"} {
				if value, set := assignments[key]; set {
					// The command's own value replaces the inherited one.
					carriers = slices.DeleteFunc(carriers, func(carrier javaHandWrittenCarrier) bool { return carrier.name == key })
					carriers = append([]javaHandWrittenCarrier{{key, value}}, carriers...)
				}
			}
			for _, word := range words[1:] {
				for _, property := range []string{"argLine", "org.gradle.jvmargs"} {
					if value, ok := strings.CutPrefix(word, "-D"+property+"="); ok {
						carriers = append([]javaHandWrittenCarrier{{property, value}}, carriers...)
					}
				}
			}
			break
		}
	}
	for _, file := range []struct{ name, property string }{{"gradle.properties", "org.gradle.jvmargs"}, {".mvn/jvm.config", ""}} {
		if value := javaHandWrittenFileOptions(filepath.Join(root, directory, filepath.FromSlash(file.name)), file.property); value != "" {
			carriers = append(carriers, javaHandWrittenCarrier{file.name, value})
		}
	}
	type attachment struct {
		carrier javaHandWrittenCarrier
		jar     string
	}
	var attachments []attachment
	for _, current := range carriers {
		for _, jar := range javaHandWrittenAgents(current.name, current.value) {
			attachments = append(attachments, attachment{current, jar})
		}
	}
	if len(attachments) == 0 {
		return nil
	}
	carrier, jar := attachments[0].carrier, attachments[0].jar
	setup := &handWrittenSetup{Reference: handWrittenReference("dd-java-agent", carrier.name)}
	if len(attachments) > 1 {
		var listed []string
		for _, attached := range attachments {
			listed = append(listed, "-javaagent:"+attached.jar+" in "+attached.carrier.name)
		}
		javaSimpleFormGap(setup, "attaches dd-java-agent more than once ("+strings.Join(listed, "; ")+")")
	}
	if _, dynamic := handWrittenReads(carrier.value); dynamic && !strings.Contains(jar, "$") {
		setup.unresolved(carrier.name + " (" + carrier.value + ") holds values only CI knows, so ddtest cannot tell which options it passes the JVM.")
	}
	switch {
	case carrier.name == "argLine":
		setup.unresolved("-DargLine attaches dd-java-agent to the forked test JVMs only, not to the Maven build that Datadog's documented setup instruments (MAVEN_OPTS), so ddtest cannot tell whether it reports the build's test sessions and modules.")
	case tool == javaGradle && (carrier.name == "org.gradle.jvmargs" || carrier.name == "gradle.properties"):
		setup.unresolved(carrier.name + " attaches dd-java-agent through org.gradle.jvmargs, which local validation cannot remove from the build, so it cannot run the build without CI's agent; attach the agent through GRADLE_OPTS or JAVA_TOOL_OPTIONS to validate this setup.")
	case carrier.name == ".mvn/jvm.config":
		setup.unresolved(".mvn/jvm.config attaches dd-java-agent to every Maven build, local ones included, so local validation cannot run the build without it.")
	case tool == javaGradle && (carrier.name == "MAVEN_OPTS"):
		setup.missing("MAVEN_OPTS attaches dd-java-agent, but this job runs Gradle, which does not read it: attach the agent through GRADLE_OPTS, org.gradle.jvmargs or JAVA_TOOL_OPTIONS instead.")
	case tool == javaMaven && (carrier.name == "GRADLE_OPTS" || carrier.name == "org.gradle.jvmargs" || carrier.name == "gradle.properties" || carrier.name == "JAVA_OPTS"):
		setup.missing(carrier.name + " attaches dd-java-agent, but this job runs Maven, which does not read it: attach the agent through MAVEN_OPTS or JAVA_TOOL_OPTIONS instead.")
	}
	// With more than one agent attached, no one release is what the JVM runs.
	if len(attachments) == 1 {
		if version := javaHandWrittenTracer(setup, jar, workflow, job, index, row); version != "" {
			setup.Tracer = version
			setup.Reference = handWrittenReference("dd-java-agent "+version, carrier.name)
		}
	}
	// -Ddd.* system properties configure the agent like DD_* variables, and
	// take precedence over them, from every JVM option list the build reads,
	// not only the one that attaches it; a carrier outside the environment,
	// as org.gradle.jvmargs, comes last.
	properties, _ := javaEffectiveProperties(commandEnv, tool)
	configuration := mergeEnvironment(commandEnv, properties)
	if _, environment := commandEnv[carrier.name]; !environment {
		configuration = mergeEnvironment(configuration, javaHandWrittenProperties(carrier.name, carrier.value))
	}
	// dd-java-agent reports tests with APM tracing off.
	delete(configuration, "DD_TRACE_ENABLED")
	handWrittenConfiguration(setup, provider, job, configuration, site, false, reportingFromEnvironment)
	return setup
}

// javaAgentCommand is a command of a job's script that names a
// dd-java-agent JAR: its source text, its words, and whether it may not run.
type javaAgentCommand struct {
	command string
	words   []string
	// conditional reports a command that may not run, or not at that point:
	// inside if, case, a loop, a function, a subshell, a pipeline, after &&
	// or ||, in the background, or in a command substitution.
	conditional bool
}

// javaAgentCommands are the commands of script that name jar or any
// dd-java-agent JAR, in an argument or a redirection, read from the
// script's shell syntax tree with env's known values, so a here-document's
// body is data, never commands. The Maven or Gradle build and the
// assignments of JVM option lists, which attach the agent, are not among
// them. changesDirectory reports a cd or pushd. ok is false for a script
// the parser cannot read.
func javaAgentCommands(script string, env map[string]string, jar string) (commands []javaAgentCommand, changesDirectory, ok bool) {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(script), "")
	if err != nil {
		return nil, false, false
	}
	// Words expand with the script's state where they are: an assignment
	// changes the commands after it, and one that may not run leaves its
	// variable unknown.
	env = maps.Clone(env)
	if env == nil {
		env = map[string]string{}
	}
	word := func(w *syntax.Word) string {
		value, _ := ciShellWord(w, env, "", "")
		return value
	}
	assign := func(assigns []*syntax.Assign, conditional bool) {
		for _, assignment := range assigns {
			if assignment.Name == nil {
				continue
			}
			if conditional || assignment.Value == nil {
				delete(env, assignment.Name.Value)
				continue
			}
			env[assignment.Name.Value] = word(assignment.Value)
		}
	}
	names := func(value string) bool {
		for _, candidate := range []string{value, value[strings.LastIndexAny(value, "=:")+1:]} {
			base := strings.ToLower(filepath.Base(candidate))
			if candidate == jar || strings.Contains(base, "dd-java-agent") && strings.HasSuffix(base, ".jar") {
				return true
			}
		}
		return false
	}
	source := func(node syntax.Node) string {
		if node.Pos().IsValid() && node.End().IsValid() && int(node.End().Offset()) <= len(script) {
			return script[node.Pos().Offset():node.End().Offset()]
		}
		return ""
	}
	var statement func(stmt *syntax.Stmt, conditional bool)
	// nested visits the statements inside node, as a command substitution's,
	// each of which may not run.
	nested := func(node syntax.Node) {
		syntax.Walk(node, func(child syntax.Node) bool {
			if stmt, ok := child.(*syntax.Stmt); ok {
				statement(stmt, true)
				return false
			}
			return true
		})
	}
	statement = func(stmt *syntax.Stmt, conditional bool) {
		conditional = conditional || stmt.Background || stmt.Coprocess
		named := false
		for _, redirect := range stmt.Redirs {
			if redirect.Word != nil && names(word(redirect.Word)) {
				named = true
			}
			nested(redirect)
		}
		var words []string
		switch command := stmt.Cmd.(type) {
		case *syntax.CallExpr:
			for _, assign := range command.Assigns {
				nested(assign)
			}
			// An assignment alone sets the shell's variable, as a JVM option
			// list that attaches the agent; one before a command sets it for
			// that command only.
			if len(command.Args) == 0 {
				assign(command.Assigns, conditional)
			}
			for _, argument := range command.Args {
				words = append(words, word(argument))
				nested(argument)
			}
			if len(words) > 0 && slices.Contains([]string{"cd", "pushd"}, filepath.Base(words[0])) {
				changesDirectory = true
			}
			if len(words) > 0 && !mavenCommand(words) && !gradle.Command(words) && slices.ContainsFunc(words, names) {
				named = true
			}
		case *syntax.Block:
			for _, inner := range command.Stmts {
				statement(inner, conditional)
			}
		case *syntax.DeclClause:
			assign(command.Args, conditional)
			for _, argument := range command.Args {
				nested(argument)
			}
		case nil:
		default:
			nested(command)
		}
		if named {
			commands = append(commands, javaAgentCommand{command: strings.TrimSpace(source(stmt)), words: words, conditional: conditional})
		}
	}
	for _, stmt := range file.Stmts {
		statement(stmt, false)
	}
	return commands, changesDirectory, true
}

// javaSimpleDownload reads a command of the simple form's download: curl
// with -o, --output or a short option group ending in o, or wget with -O,
// --output-document or a group ending in O, each followed by the output path
// as a word of its own, one URL, and no option that adds a directory.
// problem explains a command of another form.
func javaSimpleDownload(words []string) (address, output, problem string) {
	if len(words) == 0 {
		return "", "", "names the agent JAR in a command ddtest cannot read"
	}
	name := filepath.Base(words[0])
	if name != "curl" && name != "wget" {
		return "", "", "gets it with " + name + ", not curl -o or wget -O"
	}
	flag := map[string]string{"curl": "o", "wget": "O"}[name]
	long := map[string]string{"curl": "--output", "wget": "--output-document"}[name]
	var addresses []string
	for i := 1; i < len(words); i++ {
		word := words[i]
		short := strings.HasPrefix(word, "-") && !strings.HasPrefix(word, "--")
		switch {
		case strings.Contains(word, "://"):
			addresses = append(addresses, word)
		case slices.ContainsFunc([]string{"--output-dir", "--create-dirs", "--directory-prefix", "-P"}, func(option string) bool {
			return word == option || strings.HasPrefix(word, option+"=") || option == "-P" && short && strings.Contains(word, "P")
		}):
			return "", "", "downloads with " + word + ", which changes where the file lands"
		case word == long || short && strings.HasSuffix(word, flag):
			if i+1 == len(words) {
				return "", "", "names no output after " + word
			}
			i++
			output = words[i]
		}
	}
	switch {
	case output == "":
		return "", "", "downloads it without " + long + " <path> as a separate word"
	case len(addresses) != 1:
		return "", "", "downloads " + strconv.Itoa(len(addresses)) + " URLs in one command"
	}
	return addresses[0], output, ""
}

// javaSimpleFormGap leaves setup uncertified: ddtest validates a
// hand-written setup only in its simple form, which detail says how this
// one departs from.
func javaSimpleFormGap(setup *handWrittenSetup, detail string) {
	setup.unresolved("ddtest certifies a hand-written dd-java-agent setup only in its simple form: one -javaagent:<path> and, before the tests, one unconditional curl -o <path> or wget -O <path> of an exact release from Maven Central or the DataDog/dd-trace-java GitHub releases to that same path, in the test step's working directory and without cd; this job's setup " + detail + ", so it is reported but not validated.")
}

// javaHandWrittenAgents are the dd-java-agent JARs a JVM option list
// attaches (-javaagent:<path>).
func javaHandWrittenAgents(carrier, options string) []string {
	var jars []string
	for _, word := range platform.JavaCarrierWords(carrier, options) {
		jar, ok := strings.CutPrefix(platform.JavaCarrierValue(carrier, word), "-javaagent:")
		if !ok {
			continue
		}
		jar, _, _ = strings.Cut(jar, "=")
		if strings.Contains(strings.ToLower(filepath.Base(jar)), "dd-java-agent") {
			jars = append(jars, jar)
		}
	}
	return jars
}

// javaHandWrittenProperties are the DD_* variables an option list's -Ddd.*
// system properties set, such as -Ddd.civisibility.enabled=true for
// DD_CIVISIBILITY_ENABLED.
func javaHandWrittenProperties(carrier, options string) map[string]string {
	properties := map[string]string{}
	for _, word := range platform.JavaCarrierWords(carrier, options) {
		if key, value, ok := javaAgentProperty(carrier, word); ok {
			properties[key] = value
		}
	}
	return properties
}

// javaAgentProperty reads a JVM option that sets a dd-java-agent system
// property, -Ddd.<name>=<value>, as the DD_* variable it stands for and its
// value; ok is false for any other option.
func javaAgentProperty(carrier, word string) (key, value string, ok bool) {
	property, ok := strings.CutPrefix(platform.JavaCarrierValue(carrier, word), "-Ddd.")
	if !ok {
		return "", "", false
	}
	name, value, _ := strings.Cut(property, "=")
	return "DD_" + strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(name)), value, true
}

// javaHandWrittenFileOptions reads the JVM options a build file passes the
// build: property's value in a properties file, or a whole jvm.config
// (property empty); empty when the file has none.
func javaHandWrittenFileOptions(file, property string) string {
	data, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	if property == "" {
		return strings.Join(strings.Fields(string(data)), " ")
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if ok && strings.TrimSpace(key) == property {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// javaHandWrittenTracer finds the exact dd-java-agent release jar is, for
// the simple form only (see javaSimpleFormGap): before the tests, exactly
// one command of the steps that run for row names the agent JAR, and it is
// an unconditional curl or wget of one canonical artifact (see
// javaCanonicalRelease) to jar itself, in the test step's working
// directory, with no cd or pushd. A file name alone, as
// dd-java-agent-1.67.1.jar, never says which release. It records on setup
// why none is pinned.
func javaHandWrittenTracer(setup *handWrittenSetup, jar string, workflow ciWorkflow, job runtimeJob, index int, row map[string]any) string {
	test := job.Steps[index]
	var named []javaAgentCommand
	var download runtimeStep
	for i, step := range job.Steps[:index+1] {
		// A step that does not run for this row names nothing; one whose
		// condition ddtest cannot decide may not run.
		conditional := false
		if i < index {
			active, err := stepCondition(step, row, conditionEnvironment(workflow, job, runtimeStep{}, row))
			if err == nil && !active {
				continue
			}
			conditional = err != nil
		}
		// Each step reads its own environment.
		commands, changesDirectory, readable := javaAgentCommands(step.Run, handWrittenEnvironment(workflow, job, step), jar)
		switch {
		case !readable && strings.Contains(strings.ToLower(step.Run), "dd-java-agent"):
			javaSimpleFormGap(setup, "names the agent in a step that is not shell ddtest can read")
			return ""
		case changesDirectory:
			javaSimpleFormGap(setup, "changes directory with cd or pushd before the tests")
			return ""
		}
		for _, command := range commands {
			command.conditional = command.conditional || conditional
			named, download = append(named, command), step
		}
	}
	switch len(named) {
	case 0:
		setup.missing("No command of the job downloads " + jar + " with an exact release, so ddtest cannot tell which dd-java-agent it attaches: download an exact release before the tests, such as curl -Lo " + jar + " " + javaHandWrittenDownload() + ".")
		return ""
	case 1:
	default:
		var texts []string
		for _, command := range named {
			texts = append(texts, command.command)
		}
		javaSimpleFormGap(setup, "names the agent JAR in more than one command ("+strings.Join(texts, "; ")+")")
		return ""
	}
	command := named[0]
	address, output, problem := javaSimpleDownload(command.words)
	switch {
	case command.conditional:
		javaSimpleFormGap(setup, "downloads it inside conditional shell control flow, or in a step that may not run ("+command.command+")")
	case problem != "":
		javaSimpleFormGap(setup, problem+" ("+command.command+")")
	case output != jar:
		javaSimpleFormGap(setup, "writes "+output+", not the "+jar+" the tests attach")
	case stepDirectory(workflow, job, download) != stepDirectory(workflow, job, test):
		javaSimpleFormGap(setup, "downloads it in another working directory than the tests'")
	case strings.Contains(address, "dtdg.co/latest-java-tracer") || strings.Contains(address, "/latest/"):
		setup.missing("The job downloads the latest dd-java-agent (" + address + "), so each build can attach another release: download an exact release instead, such as " + javaHandWrittenDownload() + ".")
	case strings.Contains(address, "$"):
		setup.unresolved("the job downloads dd-java-agent from " + address + ", whose release only CI knows.")
	default:
		if release, ok := javaCanonicalRelease(address); ok {
			return release
		}
		setup.missing("The job downloads dd-java-agent from " + address + ", which is not an exact release's artifact on Maven Central or the DataDog/dd-trace-java GitHub releases, so ddtest cannot tell which agent it attaches: download an exact release from one of them instead, such as " + javaHandWrittenDownload() + ".")
	}
	return ""
}
