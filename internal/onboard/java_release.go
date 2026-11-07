// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// javaAgentMetadata is Maven Central's metadata of dd-java-agent: its
// release element names the latest release.
const javaAgentMetadata = "https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/maven-metadata.xml"

// javaReleaseResolver reads the latest exact dd-java-agent release. Tests
// replace it (see SetJavaReleaseResolver), so they never reach the network.
var javaReleaseResolver = latestJavaRelease

// javaRelease is the dd-java-agent release this run prescribes, once
// selected or resolved.
var javaRelease struct {
	sync.Mutex
	value string
}

// SetJavaReleaseResolver replaces how ddtest reads the latest dd-java-agent
// release, forgetting any release already selected, and returns the call
// that restores the previous resolver. Tests use it to stay offline.
func SetJavaReleaseResolver(resolve func(context.Context) (string, error)) (restore func()) {
	javaRelease.Lock()
	defer javaRelease.Unlock()
	previous, selected := javaReleaseResolver, javaRelease.value
	javaReleaseResolver, javaRelease.value = resolve, ""
	return func() {
		javaRelease.Lock()
		defer javaRelease.Unlock()
		javaReleaseResolver, javaRelease.value = previous, selected
	}
}

// SelectJavaRelease selects the dd-java-agent release every Java setup
// instruction, snippet and message of this run names, which validation then
// runs as the release CI pins: requested (--tracer-version) when it is an
// exact release, otherwise, for latest or empty, the latest release on Maven
// Central (see javaSetupRelease). Any other selector is an error: a floating
// release cannot be validated.
func SelectJavaRelease(requested string) (string, error) {
	if requested != "" && requested != "latest" {
		if !exactJavaRelease.MatchString(requested) {
			return "", fmt.Errorf("the Java tracer version must be an exact dd-java-agent release, such as %s, or latest: %s", javaRecommendedTracer, requested)
		}
		javaRelease.Lock()
		defer javaRelease.Unlock()
		javaRelease.value = requested
		return requested, nil
	}
	return javaSetupRelease(), nil
}

// javaSetupRelease is the release SelectJavaRelease selected, or else the
// latest release on Maven Central, read once per run where a Java setup is
// first written, as the other languages resolve their tracer when they need
// it. When Maven Central can't be read, it is javaRecommendedTracer, an
// exact release ddtest has validated: the instructions still name one exact
// release, which validation then downloads.
func javaSetupRelease() string {
	javaRelease.Lock()
	defer javaRelease.Unlock()
	if javaRelease.value == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		release, err := javaReleaseResolver(ctx)
		if err != nil || !exactJavaRelease.MatchString(release) {
			release = javaRecommendedTracer
		}
		javaRelease.value = release
	}
	return javaRelease.value
}

// latestJavaRelease reads the latest dd-java-agent release from Maven
// Central's metadata.
func latestJavaRelease(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, javaAgentMetadata, nil)
	if err != nil {
		return "", err
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("read dd-java-agent metadata: HTTP %d", response.StatusCode)
	}
	return readJavaRelease(io.LimitReader(response.Body, 1<<20))
}

// readJavaRelease reads the latest release from Maven metadata: its release
// element, or else its latest one, which must be an exact release.
func readJavaRelease(reader io.Reader) (string, error) {
	var metadata struct {
		Versioning struct {
			Release string `xml:"release"`
			Latest  string `xml:"latest"`
		} `xml:"versioning"`
	}
	if err := xml.NewDecoder(reader).Decode(&metadata); err != nil {
		return "", fmt.Errorf("read dd-java-agent metadata: %w", err)
	}
	release := metadata.Versioning.Release
	if release == "" {
		release = metadata.Versioning.Latest
	}
	if !exactJavaRelease.MatchString(release) {
		return "", fmt.Errorf("dd-java-agent metadata names no exact release: %q", release)
	}
	return release, nil
}

// javaReleaseNote says, in a setup's notes, which dd-java-agent release the
// setup installs and why it is exact.
func javaReleaseNote(release string) string {
	return "The setup installs dd-java-agent " + release + ", the exact release ddtest selected (the latest on Maven Central, or --tracer-version), which local validation runs: write it as shown, never a floating or other release. To set up another exact release, rerun onboard with --tracer-version <release>."
}

func (javaDiscovery) selectTracerRelease(requested string) (string, error) {
	if requested == "" {
		return "", nil
	}
	return SelectJavaRelease(requested)
}
