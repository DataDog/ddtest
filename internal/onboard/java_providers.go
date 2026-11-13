// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/maven"
)

// javaStatedRuntime takes the user's statement of the JDK a CI job runs on a
// host whose JDK no configuration shows, such as a Jenkins build agent's
// (--ci-runtime java=21).
var javaStatedRuntime = &statedRuntime{
	Names:   []string{"java"},
	Display: "Java",
	Version: javaVersion,
	Example: "21 or 17.0.12",
	base:    func(job runtimeJob) string { return javaImageRuntime(job.Image) },
	configured: func(root string, workflow ciWorkflow, job runtimeJob) (string, string) {
		rows, err := runtimeMatrix(job.Strategy.Matrix)
		return installedRuntime(rows, err, func(row map[string]any) (string, string, bool) {
			if active, err := possibleJobCondition(job.If, row); err == nil && !active {
				return "", "", false
			}
			// The tests run on the last selection before them.
			version, source := "", ""
			for _, step := range stepsThroughFirstTest(job.Steps, func(step runtimeStep) bool { return javaTestStep(root, workflow, job, step, row) }) {
				if active, err := stepCondition(step, row, conditionEnvironment(workflow, job, step, row)); err != nil || !active {
					continue
				}
				if selected := step.Runtime["java"]; selected != "" {
					version, source = selected, step.Source
				} else if uses, _, _ := strings.Cut(strings.ToLower(step.Uses), "@"); uses == "actions/setup-java" {
					if selected, err := runtimeValue(step.With["java-version"], row); err == nil && selected != "" {
						version, source = selected, "actions/setup-java"
					}
				}
			}
			return version, source, true
		})
	},
	check: func(_ context.Context, root, framework string) RuntimeCheck {
		return JavaCIConfiguration(root, framework, "")
	},
	runtime:  func(finding RuntimeFinding) string { return finding.Runtime },
	testStep: javaTestStep,
}

// javaTestStep reports whether step runs any Java framework's tests on a
// matrix row, as JavaCIConfiguration resolves them.
func javaTestStep(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any) bool {
	if active, err := stepCondition(step, row, conditionEnvironment(workflow, job, step, row)); err == nil && !active {
		return false
	}
	resolved, _, err := resolveVariantStep(workflow, job, step, row, maven.FrameworkNames[0])
	if err != nil {
		resolved = step
	}
	return slices.ContainsFunc(maven.FrameworkNames, func(framework string) bool {
		return resolveJavaStep(root, workflow, job, resolved, framework).Matched
	})
}

// javaPluginSetupRequest is the Java part of a CI server plugin's setup (the
// Datadog Jenkins plugin): it downloads dd-java-agent into the workspace and
// adds it to MAVEN_OPTS and GRADLE_OPTS for every step.
func javaPluginSetupRequest(root, framework, site string, terms ciSetupTerms) ciSetupRequest {
	request := ciSetupRequest{Language: "java", Site: site}
	request.Notes = []string{
		"The " + terms.Installer + " downloads the latest dd-java-agent into the workspace (.datadog/dd-java-agent.jar, reused for 12 hours) when each build runs, unless its additionalVariables set " + javaPluginTracerURL + ". Pin an exact release, which local validation runs: add " + javaPluginTracerURL + ": '" + javaHandWrittenDownload() + "' to the datadog option's additionalVariables (dd-java-agent " + javaSetupRelease() + ", the release ddtest selected: the latest on Maven Central, or --tracer-version); the plugin then verifies the download's signature. Without it, ddtest validates locally with the latest release and reports that it floats.",
		"The " + terms.Installer + " puts -javaagent before the MAVEN_OPTS and GRADLE_OPTS of every step, joined with a space, and sets both once per build agent: do not set MAVEN_OPTS or GRADLE_OPTS in an environment directive or withEnv; extend them inside the sh script that runs the tests (export MAVEN_OPTS=\"$MAVEN_OPTS <options>\" before the test command). Do not add -javaagent options or set DD_CIVISIBILITY_ENABLED yourself. Keep the existing build files, wrappers, Maven goals or Gradle tasks, profiles, modules, plugins, JVM options, test arguments and matrices; one Maven or Gradle command runs every framework the build declares, so do not split it or filter it by " + framework + ". Local validation runs only the Maven test lifecycle or Gradle test tasks and never installs or publishes.",
		"ddtest cannot read the JDK of a Jenkins build agent: it asks the user which Java the agents run (--ci-runtime java=<version>, or <stage>:java=<version> per stage).",
		setupUnsupportedNote(root, "java", framework),
		"If a stage runs a JDK the agent cannot instrument (early-access builds, Java older than 8), tell the user: the " + terms.Installer + " instruments every stage in its scope, so keep that stage out of the datadog option's scope with the user's approval. Report it; do not drop stages or change JDK versions to obtain a pass.",
		GitReportExclusion,
	}
	request.Notes = slices.DeleteFunc(request.Notes, func(note string) bool { return note == "" })
	return request
}

// javaFloatingCaveat states the exception to an exact release: a setup
// that downloads the latest dd-java-agent when each build runs.
func javaFloatingCaveat(setup ciSetup) []string {
	return append(slices.Clone(setup.Caveats), "dd-java-agent version: the "+setup.Reference+" downloads the latest dd-java-agent when each build runs (and reuses a download for 12 hours); ddtest validated locally with the latest release when it ran. A later release can change behavior or drop support for the agents' JDK; set "+javaPluginTracerURL+" in additionalVariables to pin one.")
}
