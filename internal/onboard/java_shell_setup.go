// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"regexp"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// For Java, Datadog's installer only downloads dd-java-agent and adds it to
// the build's JVM options, which a POSIX shell does as well: an image
// without Bash, such as maven:3-alpine, whose sh cannot even parse the
// installer, runs this setup instead (see ciShellSetup). It downloads the
// exact release and its SHA256 from Maven Central, checks the JAR, and adds
// -javaagent before MAVEN_OPTS and GRADLE_OPTS, the variables the installer
// sets by default, with the settings the setup snippet exports.

// javaShellSetupCaveat tells the user what the sh setup gives up.
const javaShellSetupCaveat = "This step's image has no Bash, so ddtest's sh setup downloads dd-java-agent and attaches it in place of Datadog's installer. With a Bash-capable image, the installer would set Test Optimization up instead, which is simpler and more reliable: it is Datadog's documented setup, and it keeps up with the installer's changes."

// javaShellSetupMessage is what the step logs when the agent cannot be
// downloaded or verified: the tests still run, untraced.
const javaShellSetupMessage = "dd-java-agent could not be downloaded or verified; the tests run without Datadog Test Optimization."

var (
	// javaShellSetupRelease is the release the sh setup downloads.
	javaShellSetupRelease = regexp.MustCompile(`https://repo1\.maven\.org/maven2/com/datadoghq/dd-java-agent/([^/"\s]+)/dd-java-agent-`)
	// javaShellSetupSite is the site the sh setup reports to.
	javaShellSetupSite = regexp.MustCompile(`DD_SITE="([^"]*)"`)
)

func (javaDiscovery) shellSetup(site, release string, form snippetForm) string {
	url := "https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/" + release + "/dd-java-agent-" + release + ".jar"
	folder := form.folder
	if folder == "" {
		folder = "$PWD/.datadog"
	}
	jar, checksum := `"$DD_TRACER_FOLDER/dd-java-agent.jar"`, `"$DD_TRACER_FOLDER/dd-java-agent.jar.sha256"`
	agent := "-javaagent:$DD_TRACER_FOLDER/dd-java-agent.jar"
	return strings.Join([]string{
		"if " + form.keyGuard() + "; then",
		`  export DD_TRACER_FOLDER="` + folder + `" DD_SITE="` + site + `" DD_CIVISIBILITY_AUTO_INSTRUMENTATION_PROVIDER="` + form.provider + `" DD_CIVISIBILITY_ENABLED=true DD_CIVISIBILITY_AGENTLESS_ENABLED=true`,
		`  if mkdir -p "$DD_TRACER_FOLDER" &&`,
		"    { curl -fsSL -o " + jar + " " + url + " || wget -qO " + jar + " " + url + "; } &&",
		"    { curl -fsSL -o " + checksum + " " + url + ".sha256 || wget -qO " + checksum + " " + url + ".sha256; } &&",
		`    echo "$(cat ` + checksum + `)  $DD_TRACER_FOLDER/dd-java-agent.jar" | { sha256sum -c - || shasum -a 256 -c -; } >/dev/null 2>&1; then`,
		`    export MAVEN_OPTS="` + agent + ` $MAVEN_OPTS" GRADLE_OPTS="` + agent + ` $GRADLE_OPTS"`,
		"  else",
		`    echo "` + javaShellSetupMessage + `" >&2`,
		"  fi",
		"else",
		`  echo "` + buildkiteMissingKeyMessage + `" >&2`,
		"fi",
	}, "\n")
}

// shellSetupAt recognizes the sh setup as shellSetup writes it, with any
// site and release; a statement that downloads dd-java-agent from Maven
// Central in another shape is a setup with a problem.
func (java javaDiscovery) shellSetupAt(stmts []*syntax.Stmt, i int, form snippetForm) (ciSetup, int, bool) {
	text := shellText(stmts[i])
	release := javaShellSetupRelease.FindStringSubmatch(text)
	if _, guarded := stmts[i].Cmd.(*syntax.IfClause); !guarded || release == nil {
		return ciSetup{}, 0, false
	}
	site := javaShellSetupSite.FindStringSubmatch(text)
	setup := ciSetup{Reference: "ddtest's sh setup for dd-java-agent " + release[1], Pinned: true, withoutBash: true}
	if site == nil {
		setup.Problem = "The step sets dd-java-agent up in a form ddtest does not recognize; give it exactly the form of ddtest's sh setup, as onboard --instructions shows."
		return setup, 1, true
	}
	expected, parsed := parseCIShell(java.shellSetup(site[1], release[1], form))
	if !parsed || len(expected.Stmts) != 1 || shellText(expected.Stmts[0]) != text {
		setup.Problem = "The step sets dd-java-agent up in a form ddtest does not recognize; give it exactly the form of ddtest's sh setup, as onboard --instructions shows."
		return setup, 1, true
	}
	setup.Inputs = map[string]string{"languages": "java", "api_key": "$DD_API_KEY", "site": site[1], "java-tracer-version": release[1]}
	setup.Supported, setup.shaped = true, true
	setup.Caveats = []string{javaShellSetupCaveat}
	return setup, 1, true
}
