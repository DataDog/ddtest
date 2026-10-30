// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"errors"
	"os"
	"path/filepath"
	"slices"

	"github.com/DataDog/ddtest/internal/gradle"
	"github.com/DataDog/ddtest/internal/maven"
)

// javaProject is one project of the selected build: a Maven module or a
// Gradle project.
type javaProject struct {
	// directory is relative to the build's checkout.
	directory  string
	frameworks []string
}

// javaResult is one native test result with the test.module dd-java-agent
// reports for it: a Maven module's name, or a Gradle test task's path.
type javaResult struct {
	module string
	maven.Test
}

// javaBuild is the build the selected Maven or Gradle command runs: the
// projects it builds, and how a validation run of it is prepared, narrowed to
// the probe and read.
type javaBuild interface {
	// tool names the build tool in messages.
	tool() string
	// buildFile names the files that declare a project's dependencies.
	buildFile() string
	// checkout is the root project directories are relative to.
	checkout() string
	projects() []javaProject
	// clean removes what an earlier run left that this run would read as
	// its own results.
	clean() error
	// arguments are what a run executes: the selected arguments, or, for a
	// probe run, those narrowed to the probe's own arguments.
	arguments(args []string, probe *javaProbe) []string
	// environment prepares a run's environment.
	environment(env map[string]string)
	// probe is framework's controlled probe at target, or why it has none.
	probe(target javaProbeTarget, framework string, args []string) (javaProbe, string, error)
	// results reads a run's native results. Missing results are an error
	// errNoJavaResults wraps; incomplete explains a build that stopped for a
	// reason other than native test failures.
	results(exitCode int, output string) (results []javaResult, incomplete string, err error)
	// directory is the project directory whose tests a module label names,
	// when a probe there can join them.
	directory(module string) (string, bool)
	// javaHome is the JDK the build tool selects for itself in place of
	// JAVA_HOME or PATH, and where that is set; empty when it selects none.
	javaHome() (home, source string)
}

// javaProbeTarget is where a probe runs: a project directory, and the module
// label whose native results it joins there, such as a Gradle test task path.
type javaProbeTarget struct{ directory, module string }

// errNoJavaResults wraps a build that wrote no native result: a skipped or
// disabled probe never starts, so its run reports none.
var errNoJavaResults = errors.New("no native results")

// javaBuild reads the build the selected command runs, from the working
// directory, as Maven or Gradle reads it.
func (t *Testdrive) javaBuild(args []string) (javaBuild, error) {
	if command, _ := t.executionCommand(); gradle.Command([]string{command}) {
		return t.gradleBuild(args)
	}
	root, projects, err := t.javaReactor(args)
	if err != nil {
		return nil, err
	}
	return newMavenBuild(root, projects), nil
}

// newMavenBuild is the reactor of projects, relative to root.
func newMavenBuild(root string, projects []maven.Project) *mavenBuild {
	return &mavenBuild{root: root, reactor: projects, labels: javaModuleLabels(projects)}
}

// mavenBuild is the reactor the selected Maven command builds.
type mavenBuild struct {
	root    string
	reactor []maven.Project
	// labels map each project directory to its module label.
	labels map[string]string
}

func (b *mavenBuild) tool() string      { return "Maven" }
func (b *mavenBuild) buildFile() string { return "POM" }
func (b *mavenBuild) checkout() string  { return b.root }

func (b *mavenBuild) projects() []javaProject {
	projects := make([]javaProject, 0, len(b.reactor))
	for _, project := range b.reactor {
		projects = append(projects, javaProject{directory: project.Directory, frameworks: project.Frameworks()})
	}
	return projects
}

// clean removes the reactor's target directories. Only the reactor's own are
// build output; a directory named target elsewhere is a project input.
func (b *mavenBuild) clean() error {
	root, err := os.OpenRoot(b.root)
	if err != nil {
		return err
	}
	for _, project := range b.reactor {
		if err := root.RemoveAll(filepath.Join(project.Directory, "target")); err != nil {
			return errors.Join(err, root.Close())
		}
	}
	return root.Close()
}

// arguments narrow a probe run to Surefire: checks after the test phase, such
// as license headers or coverage gates, would judge the probe file itself.
func (b *mavenBuild) arguments(args []string, probe *javaProbe) []string {
	if probe == nil {
		return args
	}
	return append(maven.UpToTest(args), probe.args...)
}

// ~/.mavenrc and /etc/mavenrc may attach agents or change options between runs.
func (b *mavenBuild) environment(env map[string]string) {
	env["MAVEN_SKIP_RC"] = "true"
}

func (b *mavenBuild) probe(target javaProbeTarget, framework string, args []string) (javaProbe, string, error) {
	return javaProbeFor(b.root, target.directory, framework, args)
}

func (b *mavenBuild) results(exitCode int, output string) ([]javaResult, string, error) {
	tests, err := maven.ReadReports(b.root, b.reactor)
	if errors.Is(err, maven.ErrNoTestCases) || errors.Is(err, maven.ErrNoReports) {
		err = errors.Join(errNoJavaResults, err)
	}
	if err != nil {
		return nil, "", err
	}
	incomplete := ""
	if maven.IncompleteBuild(exitCode, b.reactor, tests, output) {
		incomplete = "The Maven build stopped for a reason other than native test failures, such as compilation, setup or a later module's failure, so part of the reactor did not run. Inspect the Maven output"
	}
	results := make([]javaResult, 0, len(tests))
	for _, test := range tests {
		results = append(results, javaResult{module: b.labels[test.Module], Test: test})
	}
	return results, incomplete, nil
}

func (b *mavenBuild) javaHome() (string, string) { return "", "" }

func (b *mavenBuild) directory(module string) (string, bool) {
	for directory, label := range b.labels {
		if label == module {
			return directory, true
		}
	}
	return "", false
}

// javaFrameworks are the frameworks a build's projects declare.
func javaFrameworks(build javaBuild) []string {
	var names []string
	for _, project := range build.projects() {
		names = append(names, project.frameworks...)
	}
	return slices.Compact(slices.Sorted(slices.Values(names)))
}
