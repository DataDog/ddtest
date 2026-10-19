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

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/maven"
	"github.com/DataDog/ddtest/internal/settings"
)

type Java struct{ executor commandExecutor }

func NewJava() *Java                             { return &Java{executor: &ext.DefaultCommandExecutor{}} }
func (j *Java) Name() string                     { return "java" }
func (j *Java) Detect(root string) (bool, error) { return detectAnyFile(root, "pom.xml") }
func (j *Java) DetectFramework() (framework.Framework, error) {
	var candidates []framework.Framework
	hint := settings.GetFramework()
	if hint != "" {
		for _, name := range maven.FrameworkNames {
			candidates = append(candidates, framework.NewMaven(name, j))
		}
	} else {
		projects, err := maven.Projects(".")
		if err != nil {
			return nil, err
		}
		var names []string
		for _, project := range projects {
			for _, name := range project.Frameworks() {
				if !slices.Contains(names, name) {
					names = append(names, name)
				}
			}
		}
		slices.Sort(names)
		for _, name := range names {
			candidates = append(candidates, framework.NewMaven(name, j))
		}
	}
	return selectFramework(j.Name(), hint, candidates)
}
func (j *Java) TestSkippingLevel() settings.TestSkippingLevel { return settings.TestSkippingLevelTest }
func (j *Java) SanityCheck(ctx context.Context) error {
	_, err := j.executor.CombinedOutput(ctx, "java", []string{"-version"}, nil)
	return err
}
func (j *Java) CreateTagsMap(context.Context) (map[string]string, error) {
	return map[string]string{"language": "java"}, nil
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

// JavaAgentVersion rejects non-agent downloads before they can be injected into a JVM.
func JavaAgentVersion(path string) (string, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("read dd-java-agent JAR: %w", err)
	}
	defer func() { _ = reader.Close() }()
	for _, file := range reader.File {
		if file.Name != "META-INF/MANIFEST.MF" {
			continue
		}
		stream, err := file.Open()
		if err != nil {
			return "", err
		}
		data, err := io.ReadAll(io.LimitReader(stream, 64*1024))
		_ = stream.Close()
		if err != nil {
			return "", err
		}
		if !strings.Contains(string(data), "Premain-Class: datadog.trace.bootstrap.Agent") {
			return "", fmt.Errorf("JAR is not the Datadog Java agent")
		}
		for _, line := range strings.Split(string(data), "\n") {
			if value, ok := strings.CutPrefix(line, "Implementation-Version: "); ok {
				return strings.TrimSpace(value), nil
			}
		}
	}
	return "", fmt.Errorf("datadog Java agent has no implementation version")
}

func (j *Java) DetectTracer(_ context.Context, _ TracerOptions) (string, error) {
	if directory := os.Getenv("DD_TRACER_FOLDER"); directory != "" {
		path, err := filepath.Abs(filepath.Join(directory, "dd-java-agent.jar"))
		if err != nil {
			return "", err
		}
		if _, err := JavaAgentVersion(path); err != nil {
			return "", err
		}
		return path, nil
	}
	return "", fmt.Errorf("DD_TRACER_FOLDER does not identify an existing dd-java-agent.jar")
}

var javaRelease = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[a-zA-Z0-9.-]+)?$`)

func (j *Java) TracerInstallCommand(options TracerOptions) (string, []string, error) {
	url := "https://dtdg.co/latest-java-tracer"
	if options.Version != "" && options.Version != "latest" {
		if !javaRelease.MatchString(options.Version) {
			return "", nil, fmt.Errorf("Java tracer version must be a Maven release version or latest; git refs are not supported")
		}
		url = "https://repo.maven.apache.org/maven2/com/datadoghq/dd-java-agent/" + options.Version + "/dd-java-agent-" + options.Version + ".jar"
	}
	return "curl", []string{"--fail", "--location", "--silent", "--show-error", "--output", filepath.Join(options.Directory, "dd-java-agent.jar"), url}, nil
}
func (j *Java) InstallTestdriveTracer(ctx context.Context, options TracerOptions) (TracerInstallation, error) {
	command, args, err := j.TracerInstallCommand(options)
	if err != nil {
		return TracerInstallation{}, err
	}
	if output, err := j.executor.CombinedOutput(ctx, command, args, nil); err != nil {
		return TracerInstallation{}, runtimeTagProbeError("download Java tracer", output, err)
	}
	path := filepath.Join(options.Directory, "dd-java-agent.jar")
	version, err := JavaAgentVersion(path)
	if err != nil {
		return TracerInstallation{}, err
	}
	if options.Version != "" && options.Version != "latest" && version != options.Version {
		return TracerInstallation{}, fmt.Errorf("downloaded Java tracer %s, requested %s", version, options.Version)
	}
	return TracerInstallation{Path: path}, nil
}
