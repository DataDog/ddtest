// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// javaKarateFeature is the probe feature. Its scenario records each execution
// and fails as DDTEST_PROBE_MODE asks, as javaProbeSource does, in Karate's
// JavaScript, so no step class is needed. The tag selects it past the tags a
// project's runner filters by.
const javaKarateFeature = `Feature: ` + javaProbeClass + `

  @ddtest
  Scenario: ddtestProbe
    * eval
      """
      var System = Java.type('java.lang.System');
      var Files = Java.type('java.nio.file.Files');
      var Paths = Java.type('java.nio.file.Paths');
      var FileWriter = Java.type('java.io.FileWriter');
      var path = System.getenv('DDTEST_PROBE_STATE');
      var writer = new FileWriter(path, true);
      writer.write('run\n');
      writer.close();
      var attempts = Files.readAllLines(Paths.get(path)).size();
      var mode = System.getenv('DDTEST_PROBE_MODE');
      if (mode == 'fail' || (mode == 'fail-once' && attempts == 1)) karate.fail('ddtest intentional failure');
      """
`

// javaKarateProbeFeature is where the probe feature goes, relative to a module.
var javaKarateProbeFeature = filepath.Join("src", "test", "resources", "ddtest", "DdtestValidation.feature")

// javaKarateRunner is a test class that runs features: a JUnit 5 @Karate.Test,
// a JUnit 4 runner, or Karate's parallel Runner.
var javaKarateRunner = regexp.MustCompile(`\bcom\.intuit\.karate\.(?:Runner|junit[45]\.Karate)\b|@Karate\.Test\b|@RunWith\(\s*Karate\.class\s*\)|\bRunner\.(?:path|builder)\(`)

// javaKarateMethod is a JUnit 5 @Karate.Test or JUnit @Test method, whose
// name is the first group.
var javaKarateMethod = regexp.MustCompile(`@(?:[\w.]+\.)?(?:Karate\.)?Test\b(?:\([^)]*\))?[^{;=]*?\b([\pL_$][\pL\pN_$]*)\s*\(\s*\)`)

// javaKarateProbe places the probe feature in module and selects it through
// the module's Karate runner: karate.options replaces the runner's features
// and tags, and keeps its environment and configuration. A runner that runs
// features in parallel from a JUnit test reports that test too; it is the
// probe's harness. Each method of a runner runs the probe, so only its first
// is selected.
func javaKarateProbe(checkout, module string) (javaProbe, string, error) {
	runner, method, err := javaKarateRunnerClass(filepath.Join(checkout, module))
	if err != nil {
		return javaProbe{}, "", err
	}
	if runner == "" {
		return javaProbe{}, "No Karate runner class (@Karate.Test, @RunWith(Karate.class) or Runner.path) was found in the test sources of " + javaModuleText(module) + ", so the probe feature cannot be selected alone.", nil
	}
	selection := runner
	if method != "" {
		selection += "#" + method
	}
	feature := filepath.Join(module, javaKarateProbeFeature)
	// The feature is selected by its file: test resources a POM redefines
	// may leave it off the classpath.
	return javaProbe{
		files:   []javaProbeFile{{feature, javaKarateFeature}},
		harness: runner,
		args:    []string{"-Dtest=" + selection, "-Dkarate.options=--tags @ddtest file:" + filepath.ToSlash(filepath.Join(checkout, feature)), "-Dsurefire.failIfNoSpecifiedTests=false", "-DfailIfNoTests=false", "-DskipITs"},
	}, "", nil
}

// javaKarateRunnerClass finds the first Karate runner class in a module's test
// sources, by path, and its first test method; a JUnit 4 runner has none.
func javaKarateRunnerClass(module string) (runner, method string, err error) {
	err = filepath.WalkDir(filepath.Join(module, "src", "test", "java"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if runner != "" || entry.IsDir() || !strings.HasSuffix(path, ".java") || !entry.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		if !javaKarateRunner.MatchString(text) {
			return nil
		}
		pkg := ""
		if match := javaPackage.FindStringSubmatch(text); match != nil {
			pkg = match[1]
		}
		runner = strings.TrimPrefix(pkg+"."+strings.TrimSuffix(entry.Name(), ".java"), ".")
		if match := javaKarateMethod.FindStringSubmatch(text); match != nil {
			method = match[1]
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return "", "", nil
	}
	return runner, method, err
}
