// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/gradle"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/kballard/go-shellquote"
)

// gradleBuild is the build the selected Gradle command runs.
type gradleBuild struct {
	root    string
	all     []gradle.Project
	byPaths map[string]gradle.Project
	// home is the JDK org.gradle.java.home selects, set where source says.
	home, homeSource string
	// userHome is the Gradle user home validation runs set, when nothing
	// else selects one.
	userHome string
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
	return &gradleBuild{root: root, all: projects, byPaths: byPaths, home: home, homeSource: source, userHome: gradleDefaultUserHome(args, t.configurationEnvironment)}, nil
}

// javaWorkflowRuntime are the versions of the JDKs a scope's commands run on,
// distinct and sorted: the one org.gradle.java.home selects for a Gradle
// command, on its command line or in gradle.properties, else its
// configuration's JAVA_HOME or PATH. Empty when one cannot be read; a scope
// without commands runs on JAVA_HOME's or PATH's.
func javaWorkflowRuntime(root string, scope onboard.ValidationScope) string {
	var versions []string
	for _, entry := range scope.Commands {
		version := javaCommandRuntime(root, entry)
		if version == "" {
			return ""
		}
		if !slices.Contains(versions, version) {
			versions = append(versions, version)
		}
	}
	if len(versions) == 0 {
		return javaEnvironmentInfo(nil).Version
	}
	slices.Sort(versions)
	return strings.Join(versions, ", ")
}

// javaCommandRuntime is the version of the JDK one scope command runs on.
func javaCommandRuntime(root string, entry onboard.TestCommand) string {
	words, err := shellquote.Split(entry.Command)
	if err == nil && gradle.Command(words) {
		project, err := gradle.ProjectDirectory(words[1:])
		if err != nil {
			return ""
		}
		build, err := gradle.Build(root, filepath.Join(entry.Directory, project))
		if err != nil {
			return ""
		}
		home, source, err := gradleJavaHome(filepath.Join(root, entry.Directory), filepath.Join(root, build), words[1:], entry.Environment)
		if err != nil {
			return ""
		}
		if home != "" {
			return javaHomeInfo(home, source).Version
		}
	}
	return javaEnvironmentInfo(entry.Environment).Version
}

// gradleJavaHome is the JDK org.gradle.java.home selects for the build of the
// command run from directory, in Gradle's precedence: its command line, the
// JVM options of the Gradle launcher, the user home's gradle.properties, then
// the build's. Gradle runs on that JDK, in a single-use daemon even with
// --no-daemon, and forks test JVMs from it.
func gradleJavaHome(directory, build string, args []string, configuration map[string]string) (home, source string, err error) {
	if home, ok := gradle.SystemProperties(args)["org.gradle.java.home"]; ok {
		return home, "Java from -Dorg.gradle.java.home", nil
	}
	if home, ok := gradleLauncherProperties(configuration)["org.gradle.java.home"]; ok {
		return home, "Java from -Dorg.gradle.java.home in GRADLE_OPTS or JAVA_OPTS", nil
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

// gradleLauncherProperties are the system properties the Gradle launcher's
// JVM options set: JAVA_OPTS, then GRADLE_OPTS, which the gradlew script
// passes after it, with a configuration's own values first.
func gradleLauncherProperties(configuration map[string]string) map[string]string {
	properties := map[string]string{}
	for _, carrier := range []string{"JAVA_OPTS", "GRADLE_OPTS"} {
		options, configured := configuration[carrier]
		if !configured {
			options = os.Getenv(carrier)
		}
		words, err := shellquote.Split(options)
		if err != nil {
			words = strings.Fields(options)
		}
		for _, word := range words {
			if property, ok := strings.CutPrefix(word, "-D"); ok {
				key, value, _ := strings.Cut(property, "=")
				properties[key] = value
			}
		}
	}
	return properties
}

// gradleAgentProperties refuses a dd-java-agent that org.gradle.jvmargs
// attaches to every build of the command run from directory: set on its
// command line, in the launcher's JVM options, or in the build's or the user
// home's gradle.properties. The baseline could not run without it.
func gradleAgentProperties(directory, build string, args []string, configuration map[string]string) error {
	const remedy = "so validation cannot run the build without it; remove that -javaagent option, as CI attaches the agent with the GitHub action"
	if value, ok := gradle.SystemProperties(args)["org.gradle.jvmargs"]; ok && withoutDatadogAgent(value) != value {
		return errors.New("the command attaches dd-java-agent to the Gradle build through -Dorg.gradle.jvmargs, " + remedy)
	}
	if value, ok := gradleLauncherProperties(configuration)["org.gradle.jvmargs"]; ok && withoutDatadogAgent(value) != value {
		return errors.New("GRADLE_OPTS or JAVA_OPTS attaches dd-java-agent to the Gradle build through -Dorg.gradle.jvmargs, " + remedy)
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
	if home := gradleDefaultUserHome(args, configuration); home != "" {
		return []string{home}
	}
	home, configured := configuration["GRADLE_USER_HOME"]
	if !configured {
		home = os.Getenv("GRADLE_USER_HOME")
	}
	return []string{home}
}

// gradleDefaultUserHome is ~/.gradle when neither the command nor the
// environment selects a Gradle user home; empty otherwise.
func gradleDefaultUserHome(args []string, configuration map[string]string) string {
	home, configured := configuration["GRADLE_USER_HOME"]
	if !configured {
		home = os.Getenv("GRADLE_USER_HOME")
	}
	if len(gradle.UserHomes(args)) > 0 || home != "" {
		return ""
	}
	user, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(user, ".gradle")
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

// environment runs Gradle with the user home whose gradle.properties
// validation reads. Gradle's default follows the JVM's user.home instead,
// which a user without a passwd entry, as in a container, resolves to "?": a
// directory inside the build that then holds every download.
func (b *gradleBuild) environment(env map[string]string) {
	if b.userHome != "" {
		env["GRADLE_USER_HOME"] = b.userHome
	}
}

func (b *gradleBuild) javaHome() (string, string) { return b.home, b.homeSource }

// gradleProbeLanguages are the source languages of class probes other than
// Java's.
var gradleProbeLanguages = map[string]string{"spock": "groovy", "scalatest": "scala", "munit": "scala"}

// probe writes the framework's probe among the sources of the test task whose
// results it joins, run alone through that task. Gradle passes its own system
// properties to no test JVM, so a probe that needs some, as Cucumber's
// features or Karate's options, gets them through JAVA_TOOL_OPTIONS.
func (b *gradleBuild) probe(target javaProbeTarget, framework string, args []string) (javaProbe, string, error) {
	project, task, ok := b.task(target.module)
	if !ok {
		return javaProbe{}, "", fmt.Errorf("no Gradle test task %s", target.module)
	}
	set, ok := gradle.TestSourceSet(b.root, project, task)
	directory := func(language string) (string, string) {
		if !ok {
			return "", "The sources of " + target.module + " are set by build logic ddtest does not read, so the probe cannot be placed and its features are not validated."
		}
		directory, found := gradle.SourceDirectory(b.root, project, set, language)
		if !found {
			return "", "The " + language + " sources of " + target.module + " are set by build logic ddtest does not read, so the probe cannot be placed and its features are not validated."
		}
		return filepath.Join(project.Directory, directory), ""
	}
	switch framework {
	case "cucumber-java":
		return b.cucumberProbe(target, project, directory, args)
	case "karate":
		return b.karateProbe(target, directory, args)
	}
	language := cmp.Or(gradleProbeLanguages[framework], "java")
	sources, reason := directory(language)
	if reason != "" {
		return javaProbe{}, reason, nil
	}
	source := javaProbeSources[framework]
	probe := javaProbe{
		files: []javaProbeFile{{filepath.Join(sources, "ddtest", filepath.Base(source.path)), source.source}},
		args:  gradle.ProbeArgs(args, target.module, javaProbeClass),
	}
	if framework == "scalatest" {
		// Suites a JUnit runner starts declare it; so does the probe.
		runner, err := javaScalaTestJUnitRunner(filepath.Join(b.root, sources))
		if err != nil {
			return javaProbe{}, "", err
		}
		if runner != "" {
			probe.files[0].source = strings.Replace(source.source, "\nclass ", "\n@org.junit.runner.RunWith(classOf["+runner+"])\nclass ", 1)
		}
	}
	return probe, "", nil
}

// cucumberProbe writes the probe feature among the task's test resources and
// its step in the glue package of the task's Cucumber runner, selected alone
// with the probe feature in place of the project's.
func (b *gradleBuild) cucumberProbe(target javaProbeTarget, project gradle.Project, directory func(string) (string, string), args []string) (javaProbe, string, error) {
	sources, reason := directory("java")
	resources, resourceReason := directory("resources")
	if reason = cmp.Or(reason, resourceReason); reason != "" {
		return javaProbe{}, reason, nil
	}
	runner, glue, suite, _, lambda, err := javaCucumberRunnerClass(filepath.Join(b.root, sources), filepath.Join(b.root, resources))
	if err != nil {
		return javaProbe{}, "", err
	}
	if runner == "" {
		return javaProbe{}, "No Cucumber runner class (@RunWith(Cucumber.class) or a JUnit Platform suite of the cucumber engine) was found in the test sources of " + target.module + ", so the probe feature cannot be selected alone.", nil
	}
	steps := javaProbeFile{filepath.Join(sources, filepath.FromSlash(strings.ReplaceAll(glue, ".", "/")), "DdtestValidationSteps.java"), javaCucumberSteps(glue, javaCucumberLambda(lambda, project.Dependencies))}
	if !suite {
		return javaProbe{
			files:      []javaProbeFile{{filepath.Join(resources, "ddtest", "DdtestValidation.feature"), javaCucumberFeature}, steps},
			args:       gradle.ProbeArgs(args, target.module, runner),
			properties: []string{"-Dcucumber.features=classpath:ddtest/DdtestValidation.feature"},
		}, "", nil
	}
	// With cucumber.features, the root cucumber engine, which Gradle runs
	// and no command line excludes, would run the probe beside the suite's.
	// The probe joins the suite's own features instead, selected by its tag,
	// so only the suite runs it, with its glue and configuration.
	features, err := javaFeatureDirectory(filepath.Join(b.root, resources))
	if err != nil {
		return javaProbe{}, "", err
	}
	if features == "" {
		return javaProbe{}, "No Cucumber feature file was found in the test resources of " + target.module + ", so the probe feature cannot join the features the suite selects.", nil
	}
	return javaProbe{
		files:      []javaProbeFile{{filepath.Join(resources, features, "DdtestValidation.feature"), javaCucumberTaggedFeature}, steps},
		args:       gradle.ProbeArgs(args, target.module, runner),
		properties: []string{"-Dcucumber.filter.tags=@ddtest"},
		filtered:   true,
	}, "", nil
}

// javaCucumberTaggedFeature is the probe feature with the tag that selects it
// among a suite's features.
var javaCucumberTaggedFeature = strings.Replace(javaCucumberFeature, "  Scenario:", "  @ddtest\n  Scenario:", 1)

// javaFeatureDirectory is the directory, relative to resources, of the first
// Cucumber feature file in it, by path; empty when it has none.
func javaFeatureDirectory(resources string) (string, error) {
	directory := ""
	err := filepath.WalkDir(resources, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if directory != "" || entry.IsDir() || !strings.HasSuffix(path, ".feature") || !entry.Type().IsRegular() {
			return nil
		}
		directory, err = filepath.Rel(resources, filepath.Dir(path))
		return err
	})
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return directory, err
}

// karateProbe writes the probe feature among the task's test resources and
// selects it through the task's Karate runner, as javaKarateProbe does.
func (b *gradleBuild) karateProbe(target javaProbeTarget, directory func(string) (string, string), args []string) (javaProbe, string, error) {
	sources, reason := directory("java")
	resources, resourceReason := directory("resources")
	if reason = cmp.Or(reason, resourceReason); reason != "" {
		return javaProbe{}, reason, nil
	}
	runner, method, err := javaKarateRunnerClass(filepath.Join(b.root, sources))
	if err != nil {
		return javaProbe{}, "", err
	}
	if runner == "" {
		return javaProbe{}, "No Karate runner class (@Karate.Test, @RunWith(Karate.class) or Runner.path) was found in the test sources of " + target.module + ", so the probe feature cannot be selected alone.", nil
	}
	selection := runner
	if method != "" {
		selection += "." + method
	}
	feature := filepath.Join(resources, "ddtest", "DdtestValidation.feature")
	return javaProbe{
		files:      []javaProbeFile{{feature, javaKarateFeature}},
		harness:    runner,
		args:       gradle.ProbeArgs(args, target.module, selection),
		properties: []string{"-Dkarate.options=--tags @ddtest file:" + filepath.ToSlash(filepath.Join(b.root, feature))},
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

// directory is the project whose test task a module label names, when the
// task's source set is one ddtest reads: a probe among its sources runs
// through that task.
func (b *gradleBuild) directory(module string) (string, bool) {
	project, task, ok := b.task(module)
	if !ok {
		return "", false
	}
	if _, ok := gradle.TestSourceSet(b.root, project, task); !ok {
		return "", false
	}
	return project.Directory, true
}

// task is the project and task name of a task path, as :app:integrationTest;
// the root project's tasks are :test and the like.
func (b *gradleBuild) task(module string) (gradle.Project, string, bool) {
	index := strings.LastIndex(module, ":")
	if index < 0 || index == len(module)-1 {
		return gradle.Project{}, "", false
	}
	path := module[:index]
	if path == "" {
		path = ":"
	}
	project, known := b.byPaths[path]
	return project, module[index+1:], known
}
