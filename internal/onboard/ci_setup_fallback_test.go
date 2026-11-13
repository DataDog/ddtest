// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// fallbackText is the setup without Bash or curl for language in form.
func fallbackText(language, version string, form snippetForm) string {
	return fallbackRequestSnippet(ciSetupRequest{Language: language, Site: "datadoghq.com", TracerVersion: version}, form).shell()
}

// The setup mirrors what install_test_visibility_v15.sh prints for each
// language with the job's own tools, in POSIX sh without a backslash (a
// Groovy string would read it as an escape), behind the comment that says
// why and recommends adding Bash and curl to the image.
func TestFallbackSetupPerLanguage(t *testing.T) {
	common := []string{
		`if [ -n "${DD_API_KEY:-}" ]; then`,
		`DD_TRACER_BASE="$PWD"`,
		`case "$DD_TRACER_BASE" in *[[:space:]]*) DD_TRACER_BASE="${TMPDIR:-/tmp}" ;; esac`,
		`export DD_TRACER_FOLDER="$DD_TRACER_BASE/.datadog" DD_SITE="datadoghq.com" DD_CIVISIBILITY_AUTO_INSTRUMENTATION_PROVIDER="gitlab"`,
		`if mkdir -p "$DD_TRACER_FOLDER" &&`,
		`export DD_CIVISIBILITY_ENABLED=true DD_CIVISIBILITY_AGENTLESS_ENABLED=true DD_ENV="${DD_ENV:-ci}"`,
		`echo "` + buildkiteFailedInstallMessage + `" >&2`,
		`echo "` + gitLabMissingKeyMessage + `" >&2`,
	}
	for _, tc := range []struct {
		language, version string
		expected          []string
	}{
		{"js", "5.40.0", []string{`DD_TRACER_VERSION_JS="5.40.0"`, `NPM_CONFIG_PREFIX="$DD_TRACER_FOLDER" npm install -g "dd-trace@$DD_TRACER_VERSION_JS" >&2; then`, `export NODE_OPTIONS="${NODE_OPTIONS:-} -r $DD_TRACER_FOLDER/lib/node_modules/dd-trace/ci/init" DD_TRACE_ESM_IMPORT="$DD_TRACER_FOLDER/lib/node_modules/dd-trace/register.js"`}},
		{"python", "3.12.2", []string{`DD_TRACER_VERSION_PYTHON="3.12.2"`, `python -m pip install --upgrade --target "$DD_TRACER_FOLDER/python" "ddtrace==$DD_TRACER_VERSION_PYTHON" "coverage==` + pythonFallbackCoverage + `" >&2; then`, `export PYTHONPATH="$DD_TRACER_FOLDER/python${PYTHONPATH:+:$PYTHONPATH}" PYTEST_ADDOPTS="--ddtrace ${PYTEST_ADDOPTS:-}"`}},
		{"ruby", "1.42.0", []string{`DD_TRACER_VERSION_RUBY="1.42.0"`, `{ bundle info datadog-ci >/dev/null 2>&1 || { bundle config set frozen false >&2 && bundle add datadog-ci -v "$DD_TRACER_VERSION_RUBY" >&2; }; }; then`, `export RUBYOPT="` + rubyInstallerPreload + `"`}},
		{"java", "1.67.1", []string{`DD_TRACER_VERSION_JAVA="1.67.1"`, `{ { { curl -fsSL -o "$DD_TRACER_FOLDER/dd-java-agent.jar" ` + javaFallbackURL + ` || wget -qO "$DD_TRACER_FOLDER/dd-java-agent.jar" ` + javaFallbackURL + `; } &&`, `| { sha256sum -c - || shasum -a 256 -c -; } >/dev/null 2>&1; } ||`, `{ printf '%s' '` + javaFallbackDownload + `' >"$DD_TRACER_FOLDER/DatadogAgentDownload.java" &&`, `java "$DD_TRACER_FOLDER/DatadogAgentDownload.java" "$DD_TRACER_VERSION_JAVA" "$DD_TRACER_FOLDER/dd-java-agent.jar" >&2; }; }; then`, `export GRADLE_OPTS="-javaagent:$DD_TRACER_FOLDER/dd-java-agent.jar ${GRADLE_OPTS:-}" MAVEN_OPTS="-javaagent:$DD_TRACER_FOLDER/dd-java-agent.jar ${MAVEN_OPTS:-}" SBT_OPTS="-javaagent:$DD_TRACER_FOLDER/dd-java-agent.jar ${SBT_OPTS:-}" ANT_OPTS="-javaagent:$DD_TRACER_FOLDER/dd-java-agent.jar ${ANT_OPTS:-}"`}},
	} {
		t.Run(tc.language, func(t *testing.T) {
			text := fallbackText(tc.language, tc.version, gitLabSnippetForm)
			require.True(t, strings.HasPrefix(text, strings.Join(fallbackComment, "\n")+"\n"), text)
			require.NotContains(t, text, `\`)
			body := strings.TrimPrefix(text, strings.Join(fallbackComment, "\n"))
			// Java tries curl or wget before the JDK; the others need neither.
			if tc.language != "java" {
				require.NotContains(t, body, "curl")
				require.NotContains(t, body, "wget")
			}
			require.NotContains(t, body, "bash")
			require.Equal(t, 1, strings.Count(text, tc.version), "the release is written once")
			for _, line := range append(common, tc.expected...) {
				require.Contains(t, text, line)
			}
			file, parsed := parseCIShell(text)
			require.True(t, parsed)
			require.Len(t, file.Stmts, 1, "one guarded statement")
		})
	}
	// CircleCI's orb instruments every Java process, as ddtest reads it.
	circle := fallbackText("java", "1.67.1", circleSnippetForm)
	require.Contains(t, circle, `export JAVA_TOOL_OPTIONS="-javaagent:$DD_TRACER_FOLDER/dd-java-agent.jar ${JAVA_TOOL_OPTIONS:-}"`)
	require.NotContains(t, circle, "GRADLE_OPTS")
	// Jenkins keeps the snippet's folder of each shell and its key test.
	jenkinsText := fallbackText("js", "5.40.0", jenkinsSnippetForm)
	require.Contains(t, jenkinsText, `if [ "${DD_API_KEY:+set}" = set ]; then`)
	require.Contains(t, jenkinsText, `export DD_TRACER_FOLDER="`+strings.ReplaceAll(jenkinsTracerFolder, "$PWD", "$DD_TRACER_BASE")+`"`)
	require.Contains(t, jenkinsText, `echo "`+buildkiteMissingKeyMessage+`" >&2`)
}

// ddtest recognizes the setup it writes, in each provider's form, with its
// inputs; the comment must stand right above it.
func TestFallbackSetupIsRecognized(t *testing.T) {
	for _, form := range []snippetForm{gitLabSnippetForm, circleSnippetForm, buildkiteSnippetForm, jenkinsSnippetForm} {
		for _, language := range []string{"js", "python", "ruby", "java"} {
			text := "npm ci\n" + fallbackText(language, "1.2.3", form) + "\nnpm test\n"
			file, parsed := parseCIShell(text)
			require.True(t, parsed)
			setup, consumed, ok := fallbackSetupAt(text, file.Stmts, 1, nil, form, nil)
			require.True(t, ok, form.provider+" "+language)
			require.Equal(t, 1, consumed)
			require.True(t, setup.shaped && setup.Supported && setup.fallback, form.provider+" "+language)
			require.Empty(t, setup.Problem)
			require.Equal(t, fallbackSetupName, setup.Reference)
			require.Equal(t, map[string]string{"api_key": "$DD_API_KEY", "languages": language, "site": "datadoghq.com", language + "-tracer-version": "1.2.3"}, setup.Inputs)
			_, _, ok = fallbackSetupAt(text, file.Stmts, 0, nil, form, nil)
			require.False(t, ok, "npm ci is not the setup")
		}
	}
	text := fallbackText("ruby", "1.42.0", gitLabSnippetForm)
	parse := func(text string) (ciSetup, bool) {
		file, parsed := parseCIShell(text)
		require.True(t, parsed)
		setup, _, ok := fallbackSetupAt(text, file.Stmts, 0, nil, gitLabSnippetForm, nil)
		return setup, ok
	}
	uncommented, ok := parse(strings.Join(strings.Split(text, "\n")[2:], "\n"))
	require.True(t, ok)
	require.True(t, uncommented.shaped)
	require.Contains(t, uncommented.Problem, "lacks the comment ddtest's instructions put right above it")
	require.Contains(t, uncommented.Problem, fallbackComment[1])
	tampered, ok := parse(strings.Replace(text, "bundle add datadog-ci", "gem install datadog-ci", 1))
	require.True(t, ok)
	require.False(t, tampered.shaped, "a setup ddtest did not write is not its own")
	require.Contains(t, tampered.Problem, "in a form ddtest does not recognize")
	// Its release and site are validated like the setup snippet's.
	setup, _ := parse(strings.Replace(text, `"1.42.0"`, `"latest"`, 1))
	require.Contains(t, fallbackSetupProblem(setup, "ruby-tracer-version", exactRelease, "1.42.0", ""), `The Datadog setup without Bash or curl's DD_TRACER_VERSION_RUBY "latest" is not an exact release`)
	setup, _ = parse(text)
	require.Contains(t, fallbackSetupProblem(setup, "ruby-tracer-version", exactRelease, "1.42.0", "datadoghq.eu"), `set DD_SITE="datadoghq.eu" in it`)
	for _, provider := range []ciProvider{gitLabCI{}, circleCI{}, buildkite{}, jenkinsContainer{}, jenkins{}} {
		require.Empty(t, provider.setupProblem(setup, "ruby-tracer-version", exactRelease, "1.42.0", "datadoghq.com"), provider.name())
	}
}

// The Python setup puts its folder on PYTHONPATH before the job's, with the
// separator only when the job has one: an empty entry would put the working
// directory on Python's path. sh and ddtest's evaluation of the export agree,
// in every provider's form, and ddtest still recognizes the setup as
// complete.
func TestFallbackPythonpathHasNoEmptyEntry(t *testing.T) {
	const export = `export PYTHONPATH="$DD_TRACER_FOLDER/python${PYTHONPATH:+:$PYTHONPATH}" PYTEST_ADDOPTS="--ddtrace ${PYTEST_ADDOPTS:-}"`
	for _, form := range []snippetForm{gitLabSnippetForm, circleSnippetForm, jenkinsSnippetForm} {
		require.Contains(t, fallbackText("python", "4.16.1", form), "    "+export, form.provider)
	}
	require.Contains(t, buildkiteEscape(fallbackText("python", "4.16.1", buildkiteSnippetForm)), `export PYTHONPATH="$$DD_TRACER_FOLDER/python$${PYTHONPATH:+:$$PYTHONPATH}"`)

	file, parsed := parseCIShell(export)
	require.True(t, parsed)
	for _, tc := range []struct {
		name     string
		existing []string
		expected string
	}{
		{"unset", nil, "/w/.datadog/python"},
		{"empty", []string{""}, "/w/.datadog/python"},
		{"set", []string{"/old:src"}, "/w/.datadog/python:/old:src"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			environment := []string{"PATH=/usr/bin:/bin", "DD_TRACER_FOLDER=/w/.datadog"}
			known := map[string]string{"DD_TRACER_FOLDER": "/w/.datadog"}
			for _, value := range tc.existing {
				environment = append(environment, "PYTHONPATH="+value)
				known["PYTHONPATH"] = value
			}
			shell := exec.Command("sh", "-c", export+`; printf '%s' "$PYTHONPATH"`)
			shell.Env = environment
			output, err := shell.Output()
			require.NoError(t, err)
			require.Equal(t, tc.expected, string(output))

			state := ciShellState{dialect: circleShellDialect, env: map[string]string{}, references: map[string]string{}}
			require.True(t, state.statement(file.Stmts[0], known))
			require.Equal(t, tc.expected, state.env["PYTHONPATH"])
		})
	}

	// A GitLab job and a CircleCI job in python:3.12-slim, which has neither
	// curl nor wget, are complete with it.
	entry := indented("- |\n"+indented(fallbackText("python", "4.16.1", gitLabSnippetForm), "  "), "    ") + "\n"
	root := gitLabPythonRepository(t, "pytest:\n  image: python:3.12-slim\n  script:\n    - pip install pytest\n"+entry+"    - python -m pytest tests\n")
	require.True(t, pythonDiscovery{}.ciSetupConfigured(root, "pytest", ""))
	root = t.TempDir()
	writeFile(t, root, "pytest.ini", "[pytest]\n")
	writeFile(t, root, circleCIFile, "version: 2.1\njobs:\n  pytest:\n    docker:\n      - image: python:3.12-slim\n    steps:\n      - checkout\n      - run: pip install pytest\n      - run:\n          command: |\n"+indented(fallbackText("python", "4.16.1", circleSnippetForm), "            ")+"\n            python -m pytest tests\n"+circleWorkflow("      - pytest\n"))
	t.Chdir(root)
	useCIProvider(t, "circleci")
	require.True(t, pythonDiscovery{}.ciSetupConfigured(root, "pytest", ""))
}

// The setup runs in a job's shell, which may have set -u: every variable it
// extends may be unset there. sh -u runs it to the end in every form, and
// ddtest's evaluation of its exports agrees with sh, set or unset.
func TestFallbackSetupRunsUnderSetU(t *testing.T) {
	tools := t.TempDir()
	for _, tool := range []string{"npm", "python", "bundle", "java"} {
		require.NoError(t, os.WriteFile(filepath.Join(tools, tool), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	}
	// Java tries curl and wget first; neither reaches the network here.
	for _, tool := range []string{"curl", "wget"} {
		require.NoError(t, os.WriteFile(filepath.Join(tools, tool), []byte("#!/bin/sh\nexit 1\n"), 0o755))
	}
	carriers := map[string][]string{"js": {"NODE_OPTIONS"}, "python": {"PYTHONPATH", "PYTEST_ADDOPTS"}, "ruby": {"RUBYOPT"}, "java": {"GRADLE_OPTS", "MAVEN_OPTS", "SBT_OPTS", "ANT_OPTS", "JAVA_TOOL_OPTIONS"}}
	for _, form := range []snippetForm{gitLabSnippetForm, circleSnippetForm, buildkiteSnippetForm, jenkinsSnippetForm} {
		for _, language := range []string{"js", "python", "ruby", "java"} {
			text := fallbackText(language, "1.2.3", form)
			var export string
			for _, line := range strings.Split(text, "\n") {
				if line = strings.TrimSpace(line); strings.HasPrefix(line, "export ") && !strings.Contains(line, "DD_TRACER_FOLDER=") && !strings.Contains(line, "DD_CIVISIBILITY_ENABLED") {
					export = line
				}
			}
			require.NotEmpty(t, export, form.provider+" "+language)
			file, parsed := parseCIShell(export)
			require.True(t, parsed)
			for _, existing := range []string{"", "-existing"} {
				name := form.provider + " " + language + " " + strconv.Quote(existing)
				environment := []string{"PATH=" + tools + ":/usr/bin:/bin", "DD_API_KEY=key", "DD_TRACER_FOLDER=/w/.datadog"}
				known := map[string]string{"DD_TRACER_FOLDER": "/w/.datadog"}
				if existing != "" {
					for _, carrier := range carriers[language] {
						environment = append(environment, carrier+"="+existing)
						known[carrier] = existing
					}
				}
				shell := exec.Command("sh", "-u", "-c", text+"\necho ran")
				shell.Dir, shell.Env = t.TempDir(), environment
				output, err := shell.CombinedOutput()
				require.NoError(t, err, name+": "+string(output))
				require.Contains(t, string(output), "ran", name)

				state := ciShellState{dialect: circleShellDialect, env: map[string]string{}, references: map[string]string{}}
				require.True(t, state.statement(file.Stmts[0], known), name)
				for variable, value := range state.env {
					shell := exec.Command("sh", "-u", "-c", export+`; printf '%s' "$`+variable+`"`)
					shell.Env = environment
					output, err := shell.CombinedOutput()
					require.NoError(t, err, name+": "+string(output))
					require.Equal(t, string(output), value, name+" "+variable)
				}
			}
		}
	}
}

// The Java setup downloads the agent with curl or wget, or else with a JDK
// 11 or later; where the image has neither curl nor wget and a JRE or an
// older JDK, ddtest sets nothing up there and still recommends only that
// the user add Bash and curl.
func TestFallbackUnavailable(t *testing.T) {
	java := javaDiscovery{}
	require.Empty(t, java.ciFallbackUnavailable("openjdk:21-slim"))
	require.Empty(t, java.ciFallbackUnavailable("eclipse-temurin:17-jdk-alpine"))
	// BusyBox's wget and sha256sum download and check it on any JVM.
	require.Empty(t, java.ciFallbackUnavailable("eclipse-temurin:21-jre-alpine"))
	require.Empty(t, java.ciFallbackUnavailable("maven:3-alpine"))
	require.Empty(t, java.ciFallbackUnavailable("alpine:3.20"))
	require.Contains(t, java.ciFallbackUnavailable("openjdk:11-jre-slim"), "has only a Java runtime (JRE)")
	require.Contains(t, java.ciFallbackUnavailable("openjdk:8-jdk-slim"), "has JDK 8")
	require.Contains(t, java.ciFallbackUnavailable("debian:12-slim"), "ddtest cannot tell the JDK of debian:12-slim")
	fallback := newImageFallback(gitLabCI{}, "openjdk:8-jdk-slim", "neither curl nor wget")
	text := fallback.setupInstructions(ciSetupRequest{Language: "java", Site: "datadoghq.com", TracerInput: "java-tracer-version", TracerVersion: "1.67.1", Jobs: []string{".gitlab-ci.yml / test"}})
	require.Contains(t, text, "ddtest cannot set up Datadog Test Optimization in these jobs.")
	require.Contains(t, text, "Add no Datadog setup to them.")
	require.Contains(t, text, "it's recommended that they add Bash and curl to the image themselves")
	require.NotContains(t, text, fallbackComment[0])
	require.Contains(t, fallback.recommendation("java"), "and ddtest cannot set the job up without them either: without curl or wget, it downloads dd-java-agent")
	require.Contains(t, fallback.recommendation("js"), "so ddtest prescribes its equivalent "+fallbackSetupName+" there")
	require.Contains(t, newImageFallback(gitLabCI{}, "gcr.io/distroless/nodejs22-debian12", "no Bash").unavailable("js"), "no shell")
	require.Empty(t, newImageFallback(gitLabCI{}, "node:22-alpine", "no Bash").unavailable("js"))
}

// imageMissingTools reads what the official installer needs from the image's
// name, and what a Dockerfile or an earlier command installs.
func TestImageMissingTools(t *testing.T) {
	for _, tc := range []struct {
		image                  string
		bash, downloader, curl bool
		missing                string
	}{
		{"node:22-alpine", false, false, false, "no Bash"},
		{"node:22-slim", false, false, false, "neither curl nor wget"},
		{"node:22-slim", false, false, true, ""},
		{"node:22-slim", false, true, false, ""},
		{"node:22-alpine", true, false, false, ""},
		{"busybox", false, false, false, "no Bash"},
		{"node:22", false, false, false, ""},
		{"cimg/node:22.11", false, false, false, ""},
		{"acme/node:22-slim", false, false, false, ""},
	} {
		require.Equal(t, tc.missing, imageMissingTools(tc.image, tc.bash, tc.downloader, tc.curl), tc.image)
	}
}

func TestOfficialMavenAlpineHasBash(t *testing.T) {
	for _, image := range []string{"maven:3-alpine", "maven:3.9.9-eclipse-temurin-21-alpine", "docker.io/library/maven:3-alpine"} {
		require.Empty(t, imageMissingTools(image, false, false, false), image)
	}
	for _, image := range []string{"example/maven:3-alpine", "registry.example/maven:3-alpine", "eclipse-temurin:21-alpine", "alpine:3"} {
		require.NotEmpty(t, imageMissingTools(image, false, false, false), image)
	}
}
