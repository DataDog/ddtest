// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package platform

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/gradle"
	"github.com/DataDog/ddtest/internal/maven"
	"github.com/DataDog/ddtest/internal/settings"
)

// javaAgentJAR is the file name of the Datadog Java agent in a tracer folder.
const javaAgentJAR = "dd-java-agent.jar"

// Java runs JVM test frameworks through Maven or Gradle with the Datadog Java
// agent.
type Java struct {
	executor commandExecutor
}

func NewJava() *Java { return &Java{executor: &ext.DefaultCommandExecutor{}} }

func (j *Java) Name() string { return "java" }

func (j *Java) Detect(repositoryRoot string) (bool, error) {
	return detectAnyFile(repositoryRoot, append(append([]string{"pom.xml"}, gradle.SettingsFiles...), gradle.BuildFiles...)...)
}

// Repositories that already contain another supported project keep that
// project's platform; --platform java or a JVM --framework selects Java.
func (j *Java) fallback() bool { return true }

func (j *Java) DetectFramework() (framework.Framework, error) {
	hint := settings.GetFramework()
	names := maven.FrameworkNames
	if hint == "" {
		var err error
		if names, err = j.declaredFrameworks(); err != nil {
			return nil, err
		}
	}
	candidates := make([]framework.Framework, 0, len(names))
	for _, name := range names {
		candidates = append(candidates, framework.NewJVM(name, j))
	}
	return selectFramework(j.Name(), hint, candidates)
}

// declaredFrameworks are the frameworks the working directory's Maven
// reactor and Gradle build declare, each read only when its files exist.
func (j *Java) declaredFrameworks() ([]string, error) {
	var names []string
	if found, err := detectAnyFile(".", "pom.xml"); err != nil {
		return nil, err
	} else if found {
		projects, err := maven.Projects(".")
		if err != nil {
			return nil, err
		}
		names = maven.Frameworks(projects)
	}
	if found, err := detectAnyFile(".", append(slices.Clone(gradle.SettingsFiles), gradle.BuildFiles...)...); err != nil {
		return nil, err
	} else if found {
		build, err := gradle.Build(".", ".")
		if err != nil {
			return nil, err
		}
		projects, err := gradle.Projects(".", build)
		if err != nil {
			return nil, err
		}
		names = append(names, gradle.Frameworks(projects)...)
	}
	return slices.Compact(slices.Sorted(slices.Values(names))), nil
}

func (j *Java) TestSkippingLevel() settings.TestSkippingLevel { return settings.TestSkippingLevelTest }

func (j *Java) SanityCheck(ctx context.Context) error {
	if output, err := j.executor.CombinedOutput(ctx, "java", []string{"-version"}, nil); err != nil {
		return runtimeTagProbeError("check Java runtime", output, err)
	}
	return nil
}

func (j *Java) CreateTagsMap(context.Context) (map[string]string, error) {
	return nil, framework.ErrJVMPlanRun
}

func (j *Java) RunEnv(options framework.RuntimeOptions) (map[string]string, error) {
	if len(options.PreloadFiles) > 0 {
		return nil, fmt.Errorf("Java framework preloads are not supported")
	}
	return maps.Clone(options.Env), nil
}

func (j *Java) DiscoveryEnv(_ context.Context, _ framework.DiscoveryKind, options framework.RuntimeOptions) (map[string]string, error) {
	return j.RunEnv(options)
}

// JavaAgentVersion returns the release of the Datadog Java agent JAR at path,
// rejecting any other file before it can be attached to a JVM.
func JavaAgentVersion(path string) (string, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("read dd-java-agent JAR: %w", err)
	}
	defer func() { _ = reader.Close() }()
	manifest, err := reader.Open("META-INF/MANIFEST.MF")
	if err != nil {
		return "", fmt.Errorf("read dd-java-agent manifest: %w", err)
	}
	defer func() { _ = manifest.Close() }()
	data, err := io.ReadAll(io.LimitReader(manifest, 64*1024))
	if err != nil {
		return "", err
	}
	attributes := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if key, value, ok := strings.Cut(strings.TrimSpace(line), ": "); ok {
			attributes[key] = value
		}
	}
	if attributes["Implementation-Title"] != "dd-java-agent" || !strings.HasPrefix(attributes["Premain-Class"], "datadog.trace.bootstrap.") {
		return "", fmt.Errorf("%s is not the Datadog Java agent", path)
	}
	version := attributes["Implementation-Version"]
	if version == "" {
		return "", fmt.Errorf("the Datadog Java agent at %s has no Implementation-Version", path)
	}
	return version, nil
}

// DetectTracer reuses dd-java-agent.jar from DD_TRACER_FOLDER, where Datadog's
// CI setup installs it. Projects do not declare the agent as a dependency.
func (j *Java) DetectTracer(context.Context, TracerOptions) (string, error) {
	directory := os.Getenv("DD_TRACER_FOLDER")
	if directory == "" {
		return "", fmt.Errorf("DD_TRACER_FOLDER is not set")
	}
	path, err := filepath.Abs(filepath.Join(directory, javaAgentJAR))
	if err != nil {
		return "", err
	}
	if _, err := JavaAgentVersion(path); err != nil {
		return "", err
	}
	return path, nil
}

var javaRelease = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// TracerInstallCommand downloads a release from Maven Central, or the latest
// release through Datadog's redirect. Git builds are not distributed.
func (j *Java) TracerInstallCommand(options TracerOptions) (string, []string, error) {
	url := "https://dtdg.co/latest-java-tracer"
	if options.Version != "" && options.Version != "latest" {
		if !javaRelease.MatchString(options.Version) {
			return "", nil, fmt.Errorf("Java tracer version must be a release such as 1.67.1 or latest; git builds are not supported")
		}
		url = "https://repo.maven.apache.org/maven2/com/datadoghq/dd-java-agent/" + options.Version + "/dd-java-agent-" + options.Version + ".jar"
	}
	return "curl", []string{"--fail", "--location", "--silent", "--show-error", "--output", filepath.Join(options.Directory, javaAgentJAR), url}, nil
}

func (j *Java) InstallTestdriveTracer(ctx context.Context, options TracerOptions) (TracerInstallation, error) {
	command, args, err := j.TracerInstallCommand(options)
	if err != nil {
		return TracerInstallation{}, err
	}
	if output, err := boundedExecutor(j.executor, options.Timeout).CombinedOutput(ctx, command, args, nil); err != nil {
		return TracerInstallation{}, runtimeTagProbeError("download dd-java-agent", output, err)
	}
	path := filepath.Join(options.Directory, javaAgentJAR)
	version, err := JavaAgentVersion(path)
	if err != nil {
		return TracerInstallation{}, err
	}
	if options.Version != "" && options.Version != "latest" && version != options.Version {
		return TracerInstallation{}, fmt.Errorf("downloaded dd-java-agent %s, requested %s", version, options.Version)
	}
	return TracerInstallation{Path: path}, nil
}

// JavaOptionWords splits JVM options as the JVM reads JAVA_TOOL_OPTIONS:
// on whitespace outside quotes, so -Ddd.service="my service" is one option,
// each kept as written.
func JavaOptionWords(options string) []string {
	var words []string
	start, quote := -1, rune(0)
	for i, r := range options {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case unicode.IsSpace(r):
			if start >= 0 {
				words, start = append(words, options[start:i]), -1
			}
		default:
			if r == '"' || r == '\'' {
				quote = r
			}
			if start < 0 {
				start = i
			}
		}
	}
	if start >= 0 {
		words = append(words, options[start:])
	}
	return words
}

// JavaCarrierWords splits the JVM options of carrier as the program that
// reads it does: Maven's launcher expands MAVEN_OPTS and .mvn/jvm.config
// unquoted, so they split on whitespace with quotes kept as written; the JVM
// (JAVA_TOOL_OPTIONS), the Gradle wrapper (JAVA_OPTS, GRADLE_OPTS) and every
// other reader honor quotes (see JavaOptionWords).
func JavaCarrierWords(carrier, options string) []string {
	if carrier == "MAVEN_OPTS" || carrier == ".mvn/jvm.config" {
		return strings.Fields(options)
	}
	return JavaOptionWords(options)
}

// JavaCarrierValue is a JVM option of carrier as the JVM receives it:
// without the quotes the reader removes, or, for MAVEN_OPTS and
// .mvn/jvm.config, which Maven's launcher expands unquoted, with them kept,
// so -Ddd.civisibility.enabled="true" is "true" with its quotes.
func JavaCarrierValue(carrier, word string) string {
	if carrier == "MAVEN_OPTS" || carrier == ".mvn/jvm.config" {
		return word
	}
	return strings.NewReplacer(`"`, "", `'`, "").Replace(word)
}
