// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
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
// instructions and messages recommend.
const javaRecommendedTracer = "1.67.1"

// javaHandWrittenDownload is an exact-release download URL for messages.
const javaHandWrittenDownload = "https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/" + javaRecommendedTracer + "/dd-java-agent-" + javaRecommendedTracer + ".jar"

// javaHandWrittenCarrier is a JVM option list a hand-written setup attaches
// dd-java-agent through: its name, such as MAVEN_OPTS, and value.
type javaHandWrittenCarrier struct {
	name, value string
}

// javaHandWrittenSetup recognizes a hand-written dd-java-agent setup of the
// Maven or Gradle test command at index of job (see handWrittenSetup); nil
// when no JVM option list of the command attaches the agent.
func javaHandWrittenSetup(root string, provider ciProvider, workflow ciWorkflow, job runtimeJob, index int, framework, site string) *handWrittenSetup {
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
	var carrier javaHandWrittenCarrier
	jar := ""
	for _, current := range carriers {
		if jar = javaHandWrittenAgent(current.name, current.value); jar != "" {
			carrier = current
			break
		}
	}
	if jar == "" {
		return nil
	}
	setup := &handWrittenSetup{Reference: handWrittenReference("dd-java-agent", carrier.name)}
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
	if version := javaHandWrittenTracer(setup, jar, workflow, job, index, commandEnv); version != "" {
		setup.Tracer = version
		setup.Reference = handWrittenReference("dd-java-agent "+version, carrier.name)
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

// javaFileWrite is a command of a job's script that writes, or may write,
// a file: a curl or wget download, another command that writes files, as
// cp or tee, or an output redirection.
type javaFileWrite struct {
	// command is its source text.
	command, target string
	// addresses are the URLs a download fetches in the same command; empty
	// for any other write.
	addresses []string
	// conditional reports a command that may not run, or not at that point:
	// inside if, case, a loop, a function, a subshell, a pipeline, after &&
	// or ||, in the background, or in a command substitution.
	conditional bool
}

// javaFileWrites are the commands of script that may write a file, read
// from its shell syntax tree with env's known values, so a here-document's
// body is data, never commands. ok is false for a script the parser cannot
// read.
func javaFileWrites(script string, env map[string]string) (writes []javaFileWrite, ok bool) {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(script), "")
	if err != nil {
		return nil, false
	}
	source := func(node syntax.Node) string {
		if node.Pos().IsValid() && node.End().IsValid() && int(node.End().Offset()) <= len(script) {
			return script[node.Pos().Offset():node.End().Offset()]
		}
		return ""
	}
	word := func(w *syntax.Word) string {
		value, _ := ciShellWord(w, env, "", "")
		return value
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
		for _, redirect := range stmt.Redirs {
			if slices.Contains([]syntax.RedirOperator{syntax.RdrOut, syntax.AppOut, syntax.RdrAll, syntax.AppAll, syntax.RdrClob, syntax.RdrInOut}, redirect.Op) && redirect.Word != nil {
				writes = append(writes, javaFileWrite{command: source(stmt), target: word(redirect.Word), conditional: conditional})
			}
			nested(redirect)
		}
		switch command := stmt.Cmd.(type) {
		case *syntax.CallExpr:
			for _, assign := range command.Assigns {
				nested(assign)
			}
			var words []string
			for _, argument := range command.Args {
				words = append(words, word(argument))
				nested(argument)
			}
			for _, write := range javaCommandWrites(words) {
				write.command, write.conditional = source(stmt), conditional
				writes = append(writes, write)
			}
		case *syntax.Block:
			for _, inner := range command.Stmts {
				statement(inner, conditional)
			}
		case nil:
		default:
			nested(command)
		}
	}
	for _, stmt := range file.Stmts {
		statement(stmt, false)
	}
	return writes, true
}

// javaCommandWrites are the files a simple command, as words, writes: the
// outputs of a curl or wget download, each with every URL the command
// fetches, and the targets of the commands that copy, move, link or write
// files.
func javaCommandWrites(words []string) []javaFileWrite {
	if len(words) == 0 {
		return nil
	}
	name := filepath.Base(words[0])
	var operands []string
	for _, word := range words[1:] {
		if !strings.HasPrefix(word, "-") {
			operands = append(operands, word)
		}
	}
	switch name {
	case "curl", "wget":
		return javaDownloadWrites(name, words[1:])
	case "cp", "mv", "ln", "install", "rsync", "scp":
		if len(operands) > 1 {
			return []javaFileWrite{{target: operands[len(operands)-1]}}
		}
	case "tee":
		var writes []javaFileWrite
		for _, operand := range operands {
			writes = append(writes, javaFileWrite{target: operand})
		}
		return writes
	case "dd":
		for _, operand := range operands {
			if target, ok := strings.CutPrefix(operand, "of="); ok {
				return []javaFileWrite{{target: target}}
			}
		}
	}
	return nil
}

// javaDownloadWrites are the files a curl or wget command, by its
// arguments, writes: curl's -o/--output and -O/--remote-name outputs, or
// wget's -O/--output-document or the URL's last segment. Each carries every
// URL the command fetches, since one command with several URLs cannot say
// which output holds which.
func javaDownloadWrites(name string, arguments []string) []javaFileWrite {
	var addresses, outputs []string
	remoteName := name == "wget"
	for i := 0; i < len(arguments); i++ {
		word := arguments[i]
		short := strings.HasPrefix(word, "-") && !strings.HasPrefix(word, "--")
		switch {
		case strings.Contains(word, "://"):
			addresses = append(addresses, word)
		case name == "curl" && strings.HasPrefix(word, "--output="):
			outputs = append(outputs, strings.TrimPrefix(word, "--output="))
		case name == "curl" && (word == "--output" || short && strings.HasSuffix(word, "o")) && i+1 < len(arguments):
			// Combined short options, as -Lo or -sSLo, end with -o's value next.
			i++
			outputs = append(outputs, arguments[i])
		case name == "curl" && (word == "--remote-name" || short && strings.HasSuffix(word, "O")):
			remoteName = true
		case name == "wget" && strings.HasPrefix(word, "--output-document="):
			outputs = append(outputs, strings.TrimPrefix(word, "--output-document="))
		case name == "wget" && (word == "--output-document" || short && strings.HasSuffix(word, "O")) && i+1 < len(arguments):
			i++
			outputs = append(outputs, arguments[i])
		}
	}
	if len(addresses) == 0 {
		return nil
	}
	if len(outputs) == 0 && remoteName {
		for _, address := range addresses {
			outputs = append(outputs, address[strings.LastIndex(address, "/")+1:])
		}
	}
	writes := make([]javaFileWrite, 0, len(outputs))
	for _, output := range outputs {
		writes = append(writes, javaFileWrite{target: output, addresses: addresses})
	}
	return writes
}

// javaSameAgentPath reports whether target, which a command of
// downloadStep writes, is jar, which the test step attaches: both relative
// to their steps' working directories, or both absolute. known is false
// when ddtest cannot tell, as for an absolute path and a relative one, or a
// path that expands variables.
func javaSameAgentPath(workflow ciWorkflow, job runtimeJob, downloadStep runtimeStep, target string, testStep runtimeStep, jar string) (same, known bool) {
	if strings.ContainsAny(target+jar, "$~`") {
		return false, false
	}
	if filepath.IsAbs(target) != filepath.IsAbs(jar) {
		return false, false
	}
	resolve := func(path string, step runtimeStep) string {
		if filepath.IsAbs(path) {
			return filepath.Clean(path)
		}
		return filepath.Join(stepDirectory(workflow, job, step), path)
	}
	return resolve(target, downloadStep) == resolve(jar, testStep), true
}

// javaHandWrittenAgent is the dd-java-agent JAR a JVM option list attaches
// (-javaagent:<path>), empty when it attaches none.
func javaHandWrittenAgent(carrier, options string) string {
	for _, word := range platform.JavaCarrierWords(carrier, options) {
		jar, ok := strings.CutPrefix(platform.JavaCarrierValue(carrier, word), "-javaagent:")
		if !ok {
			continue
		}
		jar, _, _ = strings.Cut(jar, "=")
		if strings.Contains(strings.ToLower(filepath.Base(jar)), "dd-java-agent") {
			return jar
		}
	}
	return ""
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

// javaHandWrittenTracer finds the exact dd-java-agent release jar is. One
// rule establishes it: exactly one command of the job before the tests
// writes jar, and that command is an unconditional curl or wget download of
// one canonical artifact (see javaCanonicalRelease). Any other write of jar,
// or a write ddtest cannot place, leaves the release unknown, since CI may
// then attach other bytes; a file name alone, as dd-java-agent-1.67.1.jar,
// never says which. It records on setup why none is pinned.
func javaHandWrittenTracer(setup *handWrittenSetup, jar string, workflow ciWorkflow, job runtimeJob, index int, env map[string]string) string {
	var writes, unplaced []javaFileWrite
	for _, step := range job.Steps[:index+1] {
		stepWrites, readable := javaFileWrites(step.Run, env)
		if !readable {
			if strings.Contains(step.Run, filepath.Base(jar)) {
				setup.unresolved("a step that names " + filepath.Base(jar) + " is not shell ddtest can read, so it cannot tell which dd-java-agent the tests attach.")
				return ""
			}
			continue
		}
		for _, write := range stepWrites {
			if filepath.Base(write.target) != filepath.Base(jar) {
				continue
			}
			same, known := javaSameAgentPath(workflow, job, step, write.target, job.Steps[index], jar)
			switch {
			case !known:
				unplaced = append(unplaced, write)
			case same:
				writes = append(writes, write)
			}
		}
	}
	commands := func(writes []javaFileWrite) string {
		var texts []string
		for _, write := range writes {
			texts = append(texts, strings.TrimSpace(write.command))
		}
		return strings.Join(texts, "; ")
	}
	switch {
	case len(writes) == 0 && len(unplaced) > 0:
		setup.unresolved("the job writes " + unplaced[0].target + ", but ddtest cannot tell whether it is the " + jar + " the tests attach, so it cannot tell which dd-java-agent they run.")
		return ""
	case len(writes) == 0:
		setup.missing("No command of the job downloads " + jar + " with an exact release, so ddtest cannot tell which dd-java-agent it attaches: download an exact release before the tests, such as curl -Lo dd-java-agent.jar " + javaHandWrittenDownload + ".")
		return ""
	case len(writes)+len(unplaced) > 1:
		setup.unresolved("more than one command of the job may write " + jar + " (" + commands(append(writes, unplaced...)) + "), so ddtest cannot tell which dd-java-agent the tests attach; download it once, unconditionally.")
		return ""
	}
	write := writes[0]
	switch download := strings.Join(write.addresses, " "); {
	case len(write.addresses) == 0:
		setup.unresolved(jar + " comes from " + strings.TrimSpace(write.command) + ", not a download of an exact release, so ddtest cannot tell which dd-java-agent the tests attach.")
	case write.conditional:
		setup.unresolved("the job downloads " + write.target + " inside conditional shell control flow, so ddtest cannot tell whether that download runs and which dd-java-agent the tests attach.")
	case len(write.addresses) > 1:
		setup.unresolved("one command downloads several URLs (" + download + "), so ddtest cannot tell which of them " + jar + " holds; download dd-java-agent in a command of its own.")
	case strings.Contains(download, "dtdg.co/latest-java-tracer") || strings.Contains(download, "/latest/"):
		setup.missing("The job downloads the latest dd-java-agent (" + download + "), so each build can attach another release: download an exact release instead, such as " + javaHandWrittenDownload + ".")
	case strings.Contains(download, "$"):
		setup.unresolved("the job downloads dd-java-agent from " + download + ", whose release only CI knows.")
	default:
		if release, ok := javaCanonicalRelease(download); ok {
			return release
		}
		setup.missing("The job downloads dd-java-agent from " + download + ", which is not an exact release's artifact on Maven Central or the DataDog/dd-trace-java GitHub releases, so ddtest cannot tell which agent it attaches: download an exact release from one of them instead, such as " + javaHandWrittenDownload + ".")
	}
	return ""
}
