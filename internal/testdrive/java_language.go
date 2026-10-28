// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/maven"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/kballard/go-shellquote"
)

// javaLanguage validates Maven test suites with dd-java-agent. Until Java
// joins the agent workflow, it keeps the shared workflow defaults.
type javaLanguage struct{ sharedWorkflowHooks }

func (javaLanguage) name() string         { return "java" }
func (javaLanguage) frameworks() []string { return maven.FrameworkNames }
func (javaLanguage) displayNames() map[string]string {
	names := maps.Clone(maven.DisplayNames)
	names["java"] = "Java"
	return names
}
func (javaLanguage) tracerPackage() string { return "dd-java-agent" }

// The platform detects the agent JAR by path.
func (javaLanguage) installedTracerVersion(detected string) string {
	version, _ := platform.JavaAgentVersion(detected)
	return version
}

func (javaLanguage) prepareStorage() error { return nil }

// A selected command must be a literal Maven test invocation that stays inside
// the repository and publishes nothing; runs repeat it several times.
func (javaLanguage) selectCommand(request commandRequest) (string, []string, bool, error) {
	// --all validates each discovered CI command in its own copy.
	if request.all || request.override == "" {
		return "", nil, false, nil
	}
	words, err := shellquote.Split(request.override)
	if err != nil || len(words) == 0 {
		return "", nil, false, fmt.Errorf("invalid Maven command: %s", request.override)
	}
	if !maven.IsTestCommand(words[0], words[1:]) {
		return "", nil, false, fmt.Errorf("select a literal mvn or mvnw command that runs a test phase, such as mvn test or ./mvnw verify: %s", request.override)
	}
	// Validation runs only the test lifecycle: install would overwrite the
	// user's ~/.m2 artifacts, deploy would publish, and other goals run no tests.
	if validated, omitted := maven.TestInvocation(words[1:]); len(omitted) > 0 {
		return "", nil, false, fmt.Errorf("local validation runs only the Maven test lifecycle up to verify and never installs or publishes artifacts; remove %s, for example: %s", strings.Join(omitted, ", "), shellquote.Join(append([]string{words[0]}, validated...)...))
	}
	// The command runs from the repository root, which bounds its POM.
	if pom, err := maven.CommandPOM(words[1:]); err != nil {
		return "", nil, false, err
	} else if !filepath.IsLocal(pom) {
		return "", nil, false, fmt.Errorf("maven POM must stay inside the repository: %s", pom)
	}
	return words[0], words[1:], true, nil
}

// The agent JAR is read from DD_TRACER_FOLDER without running project code.
func (javaLanguage) detectsTracerInPreparation() bool                 { return true }
func (javaLanguage) refreshProjectTracer(*Testdrive, context.Context) {}
func (javaLanguage) usesPackageBinaries() bool                        { return false }

func (javaLanguage) inspectEnvironment(_ context.Context, request environmentRequest) (string, []string, environmentInfo, *reportCard) {
	return request.command, request.args, javaEnvironmentInfo(nil), nil
}

// javaEnvironmentInfo finds the JDK Maven runs on, JAVA_HOME or else the java
// on PATH, with a configuration's own values first, and reads its release
// file rather than any command's output.
func javaEnvironmentInfo(configuration map[string]string) environmentInfo {
	value := func(key string) string {
		if value, ok := configuration[key]; ok {
			return value
		}
		return os.Getenv(key)
	}
	info := environmentInfo{Source: "Java from PATH", Runtime: "Java"}
	home := value("JAVA_HOME")
	if home != "" {
		info.Source = "Java from JAVA_HOME"
	} else {
		for _, directory := range filepath.SplitList(value("PATH")) {
			candidate := filepath.Join(directory, "java")
			if stat, err := os.Stat(candidate); err != nil || stat.IsDir() || stat.Mode()&0111 == 0 {
				continue
			}
			if resolved, err := filepath.EvalSymlinks(candidate); err == nil {
				home = filepath.Dir(filepath.Dir(resolved))
			}
			break
		}
	}
	data, err := os.ReadFile(filepath.Join(home, "release"))
	if home == "" || err != nil {
		return info
	}
	for _, line := range strings.Split(string(data), "\n") {
		if version, ok := strings.CutPrefix(strings.TrimSpace(line), "JAVA_VERSION="); ok {
			info.Version, info.Executable = strings.Trim(version, `"`), home
		}
	}
	return info
}

func (javaLanguage) parsesCommandAssignments() bool { return false }

func (javaLanguage) tracerCommand(_ environmentInfo, command string, args []string) (string, []string) {
	return command, args
}

func (javaLanguage) installPreview(label string) string {
	return "download " + label + " into private temporary storage; remove it after the run"
}

func (javaLanguage) previewNotes() []string {
	return []string{
		"compare native Surefire/Failsafe results without and with dd-java-agent, then feature controls, in a disposable copy; the checkout's target directories are neither read nor changed",
		"selecting a framework labels the validation; the Maven command still runs every framework the project declares",
	}
}

func (javaLanguage) installTracer(*Testdrive, context.Context, platform.TracerOptions) (platform.TracerInstallation, bool, error) {
	return platform.TracerInstallation{}, false, nil
}

// The agent is a standalone JAR; no project file changes.
func (javaLanguage) dependencyChanges(string) ([]string, string) { return []string{"pom.xml"}, "" }

// MAVEN_OPTS is split on whitespace, so the agent path cannot contain any.
func (javaLanguage) installDirectory(sessionDirectory, _ string) (string, func() error, error) {
	if strings.ContainsAny(sessionDirectory, " \t\n") {
		return "", nil, fmt.Errorf("the temporary directory %q contains whitespace, which MAVEN_OPTS cannot carry in a -javaagent path; set TMPDIR to a directory without spaces", sessionDirectory)
	}
	return sessionDirectory, nil, nil
}

func (javaLanguage) projectTracerPath(projectTracer string) string { return projectTracer }
func (javaLanguage) temporaryTracerSource() string                 { return "temporary installation" }

// The agent rides on the Maven JVM, which configures the test JVMs Surefire
// and Failsafe fork. ScalaTest's plugin forks its own JVM, which inherits only
// JAVA_TOOL_OPTIONS, so a reactor with any ScalaTest module uses that instead.
// A configuration's own options are kept, as the baseline keeps them, except a
// Datadog agent already attached, as in a CI job after the GitHub action: the
// baseline must run without it and the reporting run with the selected one only.
func (javaLanguage) runEnvironment(t *Testdrive, path string) map[string]string {
	key := "MAVEN_OPTS"
	if t.framework.Name() == "scalatest" || t.javaReactorDeclares("scalatest") {
		key = "JAVA_TOOL_OPTIONS"
	}
	env := map[string]string{
		"DD_TRACE_ENABLED": "false",
		// Leave compiled classes as the uninstrumented build produces them.
		"DD_CIVISIBILITY_COMPILER_PLUGIN_AUTO_CONFIGURATION_ENABLED": "false",
	}
	for _, carrier := range []string{"MAVEN_OPTS", "JAVA_TOOL_OPTIONS"} {
		options, configured := t.configurationEnvironment[carrier]
		if !configured {
			options = os.Getenv(carrier)
		}
		kept := withoutDatadogAgent(options)
		if carrier == key && path != "" {
			kept = strings.TrimSpace(kept + " -javaagent:" + path)
		}
		if kept != options {
			env[carrier] = kept
		}
	}
	return env
}

// withoutDatadogAgent removes every -javaagent option of dd-java-agent from
// JVM options, by JAR name or manifest, and keeps the others as written.
func withoutDatadogAgent(options string) string {
	words := strings.Fields(options)
	kept := slices.DeleteFunc(slices.Clone(words), func(word string) bool {
		jar, ok := strings.CutPrefix(word, "-javaagent:")
		if !ok {
			return false
		}
		jar, _, _ = strings.Cut(jar, "=")
		if strings.HasPrefix(filepath.Base(jar), "dd-java-agent") {
			return true
		}
		_, err := platform.JavaAgentVersion(jar)
		return err == nil
	})
	if len(kept) == len(words) {
		return options
	}
	return strings.Join(kept, " ")
}

// javaReactorDeclares reports whether any module of the selected reactor
// declares framework.
func (t *Testdrive) javaReactorDeclares(framework string) bool {
	_, args := t.executionCommand()
	_, projects, err := t.javaReactor(args)
	return err == nil && slices.Contains(maven.Frameworks(projects), framework)
}

func (javaLanguage) noResultsFinding(reportRuntime, bool, bool, *reportCard, *intake.Advice) {}

func (javaLanguage) pairedFrameworks() map[string]pairedFramework {
	adapter := pairedFramework{
		run: func(t *Testdrive, ctx context.Context, output io.Writer, session *workspace.Session, agent string, spec runSpec) (validationRun, error) {
			return t.runJava(ctx, output, session, agent, spec, nil)
		},
		features: func(t *Testdrive, ctx context.Context, output io.Writer, session *workspace.Session, agent string, baseline validationRun, result *validationResult) error {
			return t.runJavaFeatures(ctx, output, session, agent, baseline, result)
		},
		preflight: func(t *Testdrive) func(context.Context, io.Writer, *validationResult) error {
			return t.checkJavaPreflight
		},
		telemetry: verifyJavaTelemetry,
		// The agent reports no source file for some frameworks, such as
		// Cucumber, so the probe is matched by module, class and name.
		probeTelemetry:  verifyJavaTelemetry,
		missingCoverage: "Java application coverage was not observed. dd-java-agent reports per-test coverage of the classes each test runs; inspect the selected Maven command and its JDK, and check that tests execute application classes from the repository.",
	}
	adapters := map[string]pairedFramework{}
	for _, name := range javaProbeFrameworks {
		adapters[name] = adapter
	}
	return adapters
}

// Maven plugins run project code and every run rewrites target directories,
// so each configuration runs in a disposable copy.
func (javaLanguage) isolatesConfigurations() bool { return true }

// Every Java framework, reporting-only ones included, runs in a configuration
// copy: refuse a command that would install, publish or leave the copy before
// any Maven process starts.
func (javaLanguage) prepareConfiguration(t *Testdrive, item *configurationResult, _, copyRoot string) error {
	// The command runs from the configuration's working directory.
	scoped := *t
	if item != nil {
		directory, err := localSetupDirectory(copyRoot, item.Directory)
		if err != nil {
			return err
		}
		scoped.repositoryRoot = directory
	}
	_, args := scoped.executionCommand()
	_, _, err := scoped.javaCommandPOM(args)
	return err
}

// Agents apply Maven CI setup by hand; the coordinator then validates every
// discovered configuration.
func (javaLanguage) coordinated() bool { return true }

// Local validation runs the dd-java-agent release every instrumented CI job pins.
func (javaLanguage) defaultsToCITracer() bool { return true }

func (javaLanguage) ciPinnedTracer(job onboard.RuntimeFinding) string {
	if exactJavaAgent.MatchString(job.TracerRequested) {
		return job.TracerRequested
	}
	return ""
}

// exactJavaAgent is one dd-java-agent release, never "latest".
var exactJavaAgent = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// The static inventory leaves every complete JDK and agent selection not
// checked; the preflight checks the ones this run validates.
func (javaLanguage) ciRuntimes(_ context.Context, root, framework string) onboard.RuntimeCheck {
	return onboard.JavaCIConfiguration(root, framework, "")
}

func (javaLanguage) ciRuntimeName() string { return "Java" }

func (javaLanguage) configurationsCIRuntime(t *Testdrive, _ onboard.ValidationScope) *onboard.RuntimeCheck {
	ci := onboard.JavaCIConfiguration(t.checkoutRoot(), t.framework.Name(), t.selectedSite)
	return &ci
}

func (javaLanguage) mergeCIRuntime(current, next *onboard.RuntimeCheck) *onboard.RuntimeCheck {
	return mergeJavaRuntimeChecks(current, next)
}

// The preflight labels the agent release; there is no registry selection.
func (javaLanguage) labelsTracerWithoutSelection() bool { return true }

// Reports are bound to the JDK that runs the selected Maven commands.
func (javaLanguage) workflowRuntime(*Testdrive) (string, error) {
	version := javaEnvironmentInfo(nil).Version
	if version == "" {
		return "", errors.New("no JDK was found: set JAVA_HOME to the JDK the project builds with, or put its java on PATH")
	}
	return version, nil
}

func (javaLanguage) reportRuntimeCurrent(_ string, evidence WorkflowEvidence) bool {
	return evidence.Runtime != "" && evidence.Runtime == javaEnvironmentInfo(nil).Version
}

func (javaLanguage) packageManager(string) string { return "maven" }

func (javaLanguage) packageExclusions(t *Testdrive, _ context.Context) (onboard.WorkflowCheck, bool) {
	poms, err := t.javaScopePOMs()
	if err != nil {
		return onboard.WorkflowCheck{Name: "package_report_exclusions", Status: "failed", Reason: "Cannot read the reactor of a discovered Maven command: " + reportText(err.Error())}, true
	}
	return javaPackageExclusions(t.repositoryRoot, poms), true
}

// javaScopePOMs are the POMs, relative to the repository root, of the reactors
// the discovered Maven commands build, whatever their file names.
func (t *Testdrive) javaScopePOMs() ([]string, error) {
	if t.workflowScope == nil {
		return nil, nil
	}
	var poms []string
	for _, entry := range t.workflowScope.Commands {
		words, err := shellquote.Split(entry.Command)
		if err != nil || len(words) == 0 {
			return nil, fmt.Errorf("invalid Maven command: %s", entry.Command)
		}
		pom, err := maven.CommandPOM(words[1:])
		if err != nil {
			return nil, err
		}
		projects, err := maven.ProjectsFrom(t.repositoryRoot, filepath.Join(entry.Directory, pom))
		if err != nil {
			return nil, err
		}
		for _, project := range projects {
			poms = append(poms, project.POM)
		}
	}
	return poms, nil
}

func (javaLanguage) initialization(string) string {
	return "Add the Datadog action with an exact java-tracer-version after JDK setup; it attaches dd-java-agent through MAVEN_OPTS, which later steps must extend with ${{ env.MAVEN_OPTS }}. Keep the Maven commands and the reports excluded from Git."
}

// javaReports are the retained reports, relative to the repository root.
var javaReports = []string{".testoptimization/testdrive.json", ".testoptimization/report.html"}

// javaPackageExclusions checks that no Maven artifact packages the reports.
// Maven packages only declared resources, so the reports at the repository
// root are packaged only by a resource directory that contains them and
// patterns that select them.
func javaPackageExclusions(root string, poms []string) onboard.WorkflowCheck {
	check := onboard.WorkflowCheck{Name: "package_report_exclusions", Status: "passed"}
	resources, err := maven.PackagedResources(root, poms)
	if err != nil {
		check.Status, check.Reason = "failed", "Cannot determine which files Maven packages: "+reportText(err.Error())
		return check
	}
	for _, resource := range resources {
		if strings.Contains(resource.Directory, "${") {
			check.Status, check.Reason = "failed", resource.POM+" declares the resource directory "+resource.Directory+", which ddtest cannot resolve; check that Maven does not package .testoptimization/."
			return check
		}
		for _, report := range javaReports {
			relative, err := filepath.Rel(resource.Directory, report)
			if err == nil && filepath.IsLocal(relative) && mavenResourceSelects(resource, filepath.ToSlash(relative)) {
				check.Status, check.Reason = "failed", resource.POM+" packages "+resource.Directory+" as a resource, including .testoptimization/; add <exclude>.testoptimization/**</exclude> to that resource."
				return check
			}
		}
	}
	check.Reason = fmt.Sprintf("Checked %d Maven resource declarations; none packages the reports.", len(resources))
	return check
}

// mavenResourceSelects reports whether resource's Ant patterns select path:
// every file without includes, and a directory pattern selects its contents.
// A pattern ddtest cannot read selects the path and excludes nothing.
func mavenResourceSelects(resource maven.Resource, path string) bool {
	matches := func(patterns []string, unreadable bool) bool {
		return slices.ContainsFunc(patterns, func(pattern string) bool {
			pattern = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(pattern)), "./")
			if strings.HasSuffix(pattern, "/") {
				pattern += "**"
			}
			matched, err := doublestar.Match(pattern, path)
			if err != nil {
				return unreadable
			}
			return matched
		})
	}
	return (len(resource.Includes) == 0 || matches(resource.Includes, true)) && !matches(resource.Excludes, false)
}

func init() {
	features := []featurePolicy{
		{Name: "auto-retries", Status: "required"}, {Name: "early-flake-detection", Status: "required"}, {Name: "skipping", Status: "required"}, {Name: "quarantine", Status: "required"}, {Name: "disabled", Status: "required"}, {Name: "attempt-to-fix", Status: "required"},
	}
	for name := range (javaLanguage{}).pairedFrameworks() {
		frameworkFeaturePolicies[name] = frameworkPolicy{SourceCoverage: true, ProbeBodyCounts: true, TestLevelSkipping: true, Paired: true, CheckOnly: true, Preflight: true, Configurations: true, Features: slices.Clone(features)}
	}
}
