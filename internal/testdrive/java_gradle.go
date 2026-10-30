// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/gradle"
	"github.com/kballard/go-shellquote"
)

// gradleProbeFrameworks are the frameworks whose probe class a Gradle test
// task selects with --tests.
var gradleProbeFrameworks = []string{"junit4", "junit5", "testng", "spock"}

// gradleBuild is the build the selected Gradle command runs.
type gradleBuild struct {
	root    string
	all     []gradle.Project
	byPaths map[string]gradle.Project
	// home is the JDK org.gradle.java.home selects, set where source says.
	home, homeSource string
}

// gradleBuild reads the Gradle build the selected command runs, as Gradle
// finds it from the working directory and -p. The command must request test
// tasks only, conventional or declared by the build: validation never
// publishes, and other tasks run no tests. A command that selects other build
// or settings files or adds init scripts is refused, and so is a
// dd-java-agent that org.gradle.jvmargs attaches to every build.
func (t *Testdrive) gradleBuild(args []string) (*gradleBuild, error) {
	root := t.checkoutRoot()
	directory, err := filepath.Rel(root, t.repositoryRoot)
	if err != nil {
		return nil, err
	}
	project, err := gradle.ProjectDirectory(args)
	if err != nil {
		return nil, err
	}
	invoked := filepath.Join(directory, project)
	if filepath.IsAbs(project) || !filepath.IsLocal(invoked) && invoked != "." {
		return nil, fmt.Errorf("the Gradle project directory must stay inside the repository: %s", project)
	}
	build, err := gradle.Build(root, invoked)
	if err != nil {
		return nil, err
	}
	projects, err := gradle.Projects(root, build)
	if err != nil {
		return nil, err
	}
	command, _ := t.executionCommand()
	tasks := gradle.DeclaredTestTasks(root, build, projects)
	if !gradle.RunsTests(args, tasks) {
		return nil, fmt.Errorf("select a literal gradle or gradlew command that runs test tasks, such as ./gradlew test or gradle check: %s", shellquote.Join(append([]string{command}, args...)...))
	}
	if validated, omitted := gradle.TestInvocation(args, tasks.Runs); len(omitted) > 0 {
		return nil, fmt.Errorf("local validation runs only Gradle test tasks and never publishes artifacts; remove %s, for example: %s", strings.Join(omitted, ", "), shellquote.Join(append([]string{command}, validated...)...))
	}
	// A test task the build declares may still be named like a publishing one.
	if gradle.Publishes(args) {
		return nil, errors.New("local validation runs only Gradle test tasks and never publishes, uploads or installs artifacts; remove those tasks")
	}
	if err := gradleAgentProperties(t.repositoryRoot, filepath.Join(root, build), args, t.configurationEnvironment); err != nil {
		return nil, err
	}
	home, source, err := gradleJavaHome(t.repositoryRoot, filepath.Join(root, build), args, t.configurationEnvironment)
	if err != nil {
		return nil, err
	}
	byPaths := map[string]gradle.Project{}
	for _, project := range projects {
		byPaths[project.Path] = project
	}
	return &gradleBuild{root: root, all: projects, byPaths: byPaths, home: home, homeSource: source}, nil
}

// javaWorkflowRuntime is the version of the JDK the repository's builds run
// on: the one org.gradle.java.home selects for its root Gradle build, in the
// build's or the user's gradle.properties, else JAVA_HOME's or PATH's.
func javaWorkflowRuntime(root string) string {
	if gradle.Declared(root) {
		if home, source, err := gradleJavaHome(root, root, nil, nil); err == nil && home != "" {
			return javaHomeInfo(home, source).Version
		}
	}
	return javaEnvironmentInfo(nil).Version
}

// gradleJavaHome is the JDK org.gradle.java.home selects for the build of the
// command run from directory, in Gradle's precedence: its command line, the
// user home's gradle.properties, then the build's. Gradle runs on that JDK,
// in a single-use daemon even with --no-daemon, and forks test JVMs from it.
func gradleJavaHome(directory, build string, args []string, configuration map[string]string) (home, source string, err error) {
	if home, ok := gradle.SystemProperties(args)["org.gradle.java.home"]; ok {
		return home, "Java from -Dorg.gradle.java.home", nil
	}
	var files []string
	for _, home := range gradleUserHomes(directory, args, configuration) {
		files = append(files, filepath.Join(home, "gradle.properties"))
	}
	for _, path := range append(files, filepath.Join(build, "gradle.properties")) {
		properties, err := gradle.ReadProperties(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", "", err
		}
		if home := properties["org.gradle.java.home"]; home != "" {
			return home, "Java from org.gradle.java.home in " + path, nil
		}
	}
	return "", "", nil
}

// gradleAgentProperties refuses a dd-java-agent that org.gradle.jvmargs
// attaches to every build of the command run from directory: set on its
// command line, or in the build's or the user home's gradle.properties. The
// baseline could not run without it.
func gradleAgentProperties(directory, build string, args []string, configuration map[string]string) error {
	const remedy = "so validation cannot run the build without it; remove that -javaagent option, as CI attaches the agent with the GitHub action"
	if value, ok := gradle.SystemProperties(args)["org.gradle.jvmargs"]; ok && withoutDatadogAgent(value) != value {
		return errors.New("the command attaches dd-java-agent to the Gradle build through -Dorg.gradle.jvmargs, " + remedy)
	}
	files := []string{filepath.Join(build, "gradle.properties")}
	for _, home := range gradleUserHomes(directory, args, configuration) {
		files = append(files, filepath.Join(home, "gradle.properties"))
	}
	for _, path := range files {
		properties, err := gradle.ReadProperties(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if value, ok := properties["org.gradle.jvmargs"]; ok && withoutDatadogAgent(value) != value {
			return fmt.Errorf("%s attaches dd-java-agent to every Gradle build through org.gradle.jvmargs, %s", path, remedy)
		}
	}
	return nil
}

// gradleUserHomes are the Gradle user homes a command run from directory may
// use: those its arguments name, else GRADLE_USER_HOME, else ~/.gradle.
func gradleUserHomes(directory string, args []string, configuration map[string]string) []string {
	homes := gradle.UserHomes(args)
	for i, home := range homes {
		if !filepath.IsAbs(home) {
			homes[i] = filepath.Join(directory, home)
		}
	}
	if len(homes) > 0 {
		return homes
	}
	home, configured := configuration["GRADLE_USER_HOME"]
	if !configured {
		home = os.Getenv("GRADLE_USER_HOME")
	}
	if home == "" {
		if user, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(user, ".gradle")
		}
	}
	return []string{home}
}

func (b *gradleBuild) tool() string      { return "Gradle" }
func (b *gradleBuild) buildFile() string { return "Gradle build file" }
func (b *gradleBuild) checkout() string  { return b.root }

func (b *gradleBuild) projects() []javaProject {
	projects := make([]javaProject, 0, len(b.all))
	for _, project := range b.all {
		projects = append(projects, javaProject{directory: project.Directory, frameworks: project.Frameworks()})
	}
	return projects
}

// clean removes each project's test results; validation reruns every task,
// so other output is rebuilt.
func (b *gradleBuild) clean() error {
	root, err := os.OpenRoot(b.root)
	if err != nil {
		return err
	}
	for _, project := range b.all {
		if err := root.RemoveAll(filepath.Join(project.Directory, gradle.ResultsDirectory)); err != nil {
			return errors.Join(err, root.Close())
		}
	}
	return root.Close()
}

// arguments make every run execute its tests, isolated from the others; a
// probe's arguments already run only its project's test task.
func (b *gradleBuild) arguments(args []string, probe *javaProbe) []string {
	if probe != nil {
		return probe.args
	}
	return gradle.ValidationArgs(args)
}

func (b *gradleBuild) environment(map[string]string) {}

func (b *gradleBuild) javaHome() (string, string) { return b.home, b.homeSource }

// probe writes the framework's probe class in the project at directory, run
// alone through that project's test task.
func (b *gradleBuild) probe(directory, framework string, args []string) (javaProbe, string, error) {
	if !slices.Contains(gradleProbeFrameworks, framework) {
		return javaProbe{}, "The " + displayName(framework) + " probe is not available for Gradle builds yet, so its features are not validated; the Gradle probe covers " + strings.Join(gradleProbeFrameworks, ", ") + ".", nil
	}
	index := slices.IndexFunc(b.all, func(project gradle.Project) bool { return project.Directory == directory })
	if index < 0 {
		return javaProbe{}, "", fmt.Errorf("no Gradle project in %s", directory)
	}
	source := javaProbeSources[framework]
	return javaProbe{
		files: []javaProbeFile{{filepath.Join(directory, source.path), source.source}},
		args:  gradle.ProbeArgs(args, b.all[index].Path, javaProbeClass),
	}, "", nil
}

func (b *gradleBuild) results(exitCode int, output string) ([]javaResult, string, error) {
	tests, err := gradle.ReadReports(b.root, b.all)
	if errors.Is(err, gradle.ErrNoTestCases) || errors.Is(err, gradle.ErrNoReports) {
		err = errors.Join(errNoJavaResults, err)
	}
	if err != nil {
		return nil, "", err
	}
	incomplete := ""
	if gradle.IncompleteBuild(exitCode, tests, output) {
		incomplete = "The Gradle build stopped for a reason other than native test failures, such as configuration, compilation or another task's failure, so some tasks did not run. Inspect the Gradle output"
	}
	results := make([]javaResult, 0, len(tests))
	for _, test := range tests {
		results = append(results, javaResult{module: test.Task, Test: test.Test})
	}
	return results, incomplete, nil
}

// directory is the project whose test task a module label names: a probe in
// it runs through that task. Other test tasks, such as integrationTest, run
// other source sets than the probe's.
func (b *gradleBuild) directory(module string) (string, bool) {
	path, found := strings.CutSuffix(module, ":test")
	if !found {
		return "", false
	}
	// The root project's test task is :test.
	if path == "" {
		path = ":"
	}
	project, known := b.byPaths[path]
	if !known {
		return "", false
	}
	return project.Directory, true
}
