// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/maven"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

type mavenProbe struct {
	files map[string]string
	args  []string
}

func (p mavenProbe) write(root string) error {
	directory, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	for path, content := range p.files {
		if err := directory.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		file, err := directory.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fmt.Errorf("create owned Maven probe: %w", err)
		}
		_, writeErr := file.WriteString(content)
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

const javaProbeBody = `
    public static void check() throws Exception {
        java.nio.file.Path path = java.nio.file.Paths.get(System.getenv("DDTEST_PROBE_COUNTER"));
        int attempts = java.nio.file.Files.exists(path) ? Integer.parseInt(new String(java.nio.file.Files.readAllBytes(path), java.nio.charset.StandardCharsets.UTF_8).trim()) : 0;
        attempts++;
        java.nio.file.Files.write(path, Integer.toString(attempts).getBytes(java.nio.charset.StandardCharsets.UTF_8));
        String mode = System.getenv("DDTEST_PROBE_MODE");
        if ("fail".equals(mode) || ("fail-once".equals(mode) && attempts == 1)) {
            throw new AssertionError("ddtest intentional failure");
        }
    }
`

func newMavenProbe(project maven.Project, framework string) mavenProbe {
	// A verify lifecycle may bind both plugins. Select Surefire for controls
	// and skip Failsafe so the same probe body is never executed twice.
	p := mavenProbe{files: map[string]string{}, args: []string{"-Dtest=ddtest.DdtestValidationTest", "-DskipITs=true", "-Dsurefire.failIfNoSpecifiedTests=false"}}
	java := "package ddtest;\npublic class DdtestValidationTest {\n" + javaProbeBody
	annotation := map[string]string{"junit4": "org.junit.Test", "junit5": "org.junit.jupiter.api.Test", "testng": "org.testng.annotations.Test"}[framework]
	if annotation != "" {
		java += "\n@" + annotation + " public void ddtestProbe() throws Exception { check(); }\n}\n"
		p.files[filepath.Join(project.Directory, "src/test/java/ddtest/DdtestValidationTest.java")] = java
		return p
	}
	switch framework {
	case "spock":
		p.files[filepath.Join(project.Directory, "src/test/groovy/ddtest/DdtestValidationTest.groovy")] = "package ddtest\nclass DdtestValidationTest extends spock.lang.Specification {\n" + javaProbeBody + "\ndef ddtestProbe() { expect: check(); true }\n}\n"
	case "karate":
		java += "\n@com.intuit.karate.junit5.Karate.Test public com.intuit.karate.junit5.Karate ddtestProbe() { return com.intuit.karate.junit5.Karate.run(\"ddtest\").relativeTo(getClass()); }\n}\n"
		p.files[filepath.Join(project.Directory, "src/test/java/ddtest/DdtestValidationTest.java")] = java
		p.files[filepath.Join(project.Directory, "src/test/resources/ddtest/ddtest.feature")] = "Feature: ddtest\nScenario: ddtest validation probe\n* eval Java.type('ddtest.DdtestValidationTest').check()\n"
	case "cucumber-java":
		p.args = append(p.args, "-Dcucumber.filter.name=^ddtest validation probe$")
		runner := ""
		for _, dep := range project.Dependencies {
			switch dep.Artifact {
			case "cucumber-junit":
				runner = "@org.junit.runner.RunWith(io.cucumber.junit.Cucumber.class)\n@io.cucumber.junit.CucumberOptions(features=\"classpath:ddtest/ddtest.feature\", glue=\"ddtest\")\npublic class DdtestValidationTest {}"
			case "cucumber-testng":
				runner = "@io.cucumber.testng.CucumberOptions(features=\"classpath:ddtest/ddtest.feature\", glue=\"ddtest\")\npublic class DdtestValidationTest extends io.cucumber.testng.AbstractTestNGCucumberTests {}"
			case "cucumber-junit-platform-engine":
				runner = "@org.junit.platform.suite.api.Suite\n@org.junit.platform.suite.api.IncludeEngines(\"cucumber\")\n@org.junit.platform.suite.api.SelectClasspathResource(\"ddtest\")\n@org.junit.platform.suite.api.ConfigurationParameter(key=\"cucumber.glue\", value=\"ddtest\")\npublic class DdtestValidationTest {}"
			}
		}
		if runner == "" {
			return mavenProbe{}
		}
		p.files[filepath.Join(project.Directory, "src/test/java/ddtest/DdtestValidationTest.java")] = "package ddtest;\n" + runner
		p.files[filepath.Join(project.Directory, "src/test/java/ddtest/DdtestValidationSteps.java")] = "package ddtest;\npublic class DdtestValidationSteps {\n" + javaProbeBody + "\n@io.cucumber.java.en.Given(\"ddtest probe\") public void ddtestProbe() throws Exception { check(); }\n}\n"
		p.files[filepath.Join(project.Directory, "src/test/resources/ddtest/ddtest.feature")] = "Feature: ddtest\nScenario: ddtest validation probe\nGiven ddtest probe\n"
	case "scalatest", "munit":
		base := "org.scalatest.funsuite.AnyFunSuite"
		if framework == "munit" {
			base = "munit.FunSuite"
		}
		annotation := ""
		for _, dep := range project.Dependencies {
			if framework == "scalatest" && strings.HasPrefix(dep.Artifact, "junit-4-13_") {
				annotation = "@org.junit.runner.RunWith(classOf[org.scalatestplus.junit.JUnitRunner])\n"
			}
		}
		p.files[filepath.Join(project.Directory, "src/test/scala/ddtest/DdtestValidationTest.scala")] = "package ddtest\n" + annotation + "class DdtestValidationTest extends " + base + ` {
  test("ddtestProbe") {
    val path = java.nio.file.Paths.get(sys.env("DDTEST_PROBE_COUNTER"))
    val attempts = (if (java.nio.file.Files.exists(path)) new String(java.nio.file.Files.readAllBytes(path), java.nio.charset.StandardCharsets.UTF_8).trim.toInt else 0) + 1
    java.nio.file.Files.write(path, attempts.toString.getBytes(java.nio.charset.StandardCharsets.UTF_8))
    val mode = sys.env("DDTEST_PROBE_MODE")
    if (mode == "fail" || (mode == "fail-once" && attempts == 1)) throw new AssertionError("ddtest intentional failure")
  }
}
`
		if framework == "scalatest" {
			p.args = append(p.args, "-Dsuites=ddtest.DdtestValidationTest")
		}
	}
	return p
}

func (t *Testdrive) mavenFeatures(ctx context.Context, output io.Writer, agent string, projects []maven.Project, reporting validationRun, result *validationResult) error {
	selected := t.framework.Name()
	checked := false
	for _, project := range projects {
		frameworks := project.Frameworks()
		if len(frameworks) > 0 && !slices.Contains(frameworks, selected) {
			continue
		}
		// A profile/aggregator dependency is not an executed module. Check only
		// modules that actually produced native results in the selected command.
		executed := false
		for _, test := range reporting.Tests {
			executed = executed || strings.HasPrefix(test.File, project.Directory+" / ")
		}
		if !executed {
			continue
		}
		checked = true
		probe := mavenProbe{}
		if slices.Contains(frameworks, selected) {
			probe = newMavenProbe(project, selected)
		}
		if len(probe.files) == 0 {
			for _, policy := range featurePolicies(t.framework.Name()) {
				feature := policy.Name
				result.Features = append(result.Features, featureResult{Project: project.Directory, Name: feature, Status: "inconclusive", Reason: "Cannot identify the selected framework's probe runner from declared Maven dependencies; remote parents/BOM/profile dependencies remain unresolved."})
			}
			continue
		}
		control, err := t.mavenRun(ctx, output, "probe-control", agent, intake.Scenario{}, "pass", &probe)
		if err != nil {
			return err
		}
		result.Runs = append(result.Runs, control.summary())
		if control.ExitCode != 0 || control.ResultError != "" || len(control.Tests) != 1 || len(control.Facts.Tests) != 1 || control.ProbeExecutions == nil || *control.ProbeExecutions != 1 {
			for _, policy := range featurePolicies(t.framework.Name()) {
				feature := policy.Name
				result.Features = append(result.Features, featureResult{Project: project.Directory, Name: feature, Status: "inconclusive", Reason: "The existing Maven configuration did not select exactly one controlled probe; preserve custom source roots, includes and providers and inspect the probe command diagnostics."})
			}
			continue
		}
		identity := control.Facts.Tests[0]
		advertised := false
		for _, policy := range featurePolicies(t.framework.Name()) {
			feature := policy.Name
			advertised = advertised || mavenCapability(control, identity, feature)
		}
		if !advertised {
			for _, policy := range featurePolicies(t.framework.Name()) {
				feature := policy.Name
				result.Features = append(result.Features, featureResult{Project: project.Directory, Name: feature, Status: "inconclusive", Reason: "The controlled Java event did not advertise feature capabilities; use a compatible tracer before certifying feature support."})
			}
			continue
		}
		failing, err := t.mavenRun(ctx, output, "probe-failure-control", agent, intake.Scenario{}, "fail", &probe)
		if err != nil {
			return err
		}
		result.Runs = append(result.Runs, failing.summary())
		if len(failing.Tests) != 1 || failing.Tests[0].Status != "failed" || !strings.Contains(failing.Tests[0].Failure, "ddtest intentional failure") {
			for _, policy := range featurePolicies(t.framework.Name()) {
				feature := policy.Name
				result.Features = append(result.Features, featureResult{Project: project.Directory, Name: feature, Status: "inconclusive", Reason: "The intentional failure control was not observed in native results."})
			}
			continue
		}
		for _, policy := range featurePolicies(t.framework.Name()) {
			feature := policy.Name
			if !mavenCapability(control, identity, feature) {
				result.Features = append(result.Features, featureResult{Project: project.Directory, Name: feature, Status: "not supported", Reason: "The selected Java test integration did not advertise this feature in its controlled test event; it was not executed or counted as passed."})
				continue
			}
			mode := "pass"
			if feature == "auto-retries" {
				mode = "fail-once"
			}
			if feature == "quarantine" || feature == "disabled" || feature == "skipping" {
				mode = "fail"
			}
			scenario := intake.Scenario{Feature: feature, Module: identity.Module, Suite: identity.Suite, Test: identity.Name, SourceFile: identity.SourceFile, TestLevel: "test"}
			run, err := t.mavenRun(ctx, output, feature, agent, scenario, mode, &probe)
			if err != nil {
				return err
			}
			result.Runs = append(result.Runs, run.summary())
			check := evaluateMavenFeature(feature, run, identity)
			if feature == "quarantine" && failing.ExitCode == 0 && (len(run.Tests) != 1 || run.Tests[0].Status == "failed") {
				check.Status = "inconclusive"
				check.Reason = "The failure control already returned success and quarantine did not change its native failure status; failure suppression was not established."
			}
			check.Project = project.Directory
			result.Features = append(result.Features, check)
		}
	}
	if !checked {
		unavailableFeatures(result, "No executed module has statically resolved dependencies for this framework; remote parents/BOM/profile configuration needs a probe adapter before features can be certified.")
	}
	return nil
}

func mavenCapability(run validationRun, identity intake.Test, feature string) bool {
	name := map[string]string{"auto-retries": "auto_test_retries", "early-flake-detection": "early_flake_detection", "skipping": "test_impact_analysis", "quarantine": "test_management.quarantine", "disabled": "test_management.disable", "attempt-to-fix": "test_management.attempt_to_fix"}[feature]
	for _, event := range run.Facts.Events {
		if event.Type == "test" && event.Tags["test.module"] == identity.Module && event.Tags["test.suite"] == identity.Suite && event.Tags["test.name"] == identity.Name {
			return event.Tags["_dd.library_capabilities."+name] != ""
		}
	}
	return false
}

func evaluateMavenFeature(feature string, run validationRun, identity intake.Test) featureResult {
	check := featureResult{Name: feature, Status: "failed", Reason: "Controlled Java execution counts, native results and feature telemetry did not demonstrate the expected behavior."}
	if run.ResultError != "" || len(run.Facts.ConfigurationErrors) > 0 {
		check.Status = "inconclusive"
		return check
	}
	for _, test := range run.Tests {
		if mavenTestName(test.Name) != mavenTestName(identity.Name) {
			check.Status = "inconclusive"
			check.Reason = "The feature run included tests outside the controlled probe."
			return check
		}
	}
	for _, event := range run.Facts.Events {
		if event.Type == "test" && (event.Tags["test.module"] != identity.Module || event.Tags["test.suite"] != identity.Suite || event.Tags["test.name"] != identity.Name) {
			check.Status = "inconclusive"
			check.Reason = "The feature run emitted test events outside the controlled probe."
			return check
		}
	}
	attempts := 0
	if run.ProbeExecutions != nil {
		attempts = *run.ProbeExecutions
	}
	passed, failed, retried := 0, 0, 0
	quarantined, disabled, fixing, skipped := false, false, false, false
	for _, event := range run.Facts.Events {
		tags := event.Tags
		if tags["test.module"] != identity.Module || tags["test.suite"] != identity.Suite {
			continue
		}
		skipped = skipped || tags["test.itr.tests_skipping.type"] == "test" && tags["test.itr.skipped"] == "true"
		if event.Type != "test" || tags["test.name"] != identity.Name {
			continue
		}
		if tags["test.status"] == "pass" {
			passed++
		}
		if tags["test.status"] == "fail" {
			failed++
		}
		if tags["test.is_retry"] == "true" {
			retried++
		}
		quarantined = quarantined || tags["test.test_management.is_quarantined"] == "true"
		disabled = disabled || tags["test.test_management.is_test_disabled"] == "true" && tags["test.status"] == "skip"
		fixing = fixing || tags["test.test_management.is_attempt_to_fix"] == "true"
		skipped = skipped || tags["test.skipped_by_itr"] == "true"
	}
	ok := false
	if run.ExitCode == 0 {
		switch feature {
		case "auto-retries":
			ok = attempts >= 2 && failed == 1 && passed >= 1 && retried >= 1 && executedTests(run) > 0
		case "early-flake-detection":
			ok = attempts >= 2 && failed == 0 && passed >= 2 && retried >= 1 && executedTests(run) > 0
		case "skipping":
			ok = attempts == 0 && executedTests(run) == 0 && skipped
		case "quarantine":
			ok = attempts == 1 && failed == 1 && passed == 0 && quarantined && len(run.Tests) == 1
		case "disabled":
			ok = attempts == 0 && executedTests(run) == 0 && disabled
		case "attempt-to-fix":
			ok = attempts >= 2 && passed >= 2 && failed == 0 && fixing && retried >= 1 && executedTests(run) > 0
		}
	}
	if ok {
		check.Status = "passed"
		check.Reason = "Native Maven outcomes, probe body counter and Java feature events agree with the controlled scenario."
	}
	return check
}
