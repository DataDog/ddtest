// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package framework

import (
	"context"
	"os"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestJVMCommandPrefersTheWrapper(t *testing.T) {
	t.Cleanup(func() {
		viper.Reset()
		settings.Init()
	})
	viper.Reset()
	settings.Init()
	t.Chdir(t.TempDir())
	maven := NewJVM("junit5", nil)
	command, args := maven.Command()
	require.Equal(t, "mvn", command)
	require.Equal(t, []string{"test"}, args)

	require.NoError(t, os.WriteFile("mvnw", []byte("#!/bin/sh\n"), 0755))
	command, args = maven.Command()
	require.Equal(t, "./mvnw", command)
	require.Equal(t, []string{"test"}, args)

	viper.Reset()
	viper.Set("command", "./mvnw -B verify")
	settings.Init()
	command, args = maven.Command()
	require.Equal(t, "./mvnw", command)
	require.Equal(t, []string{"-B", "verify"}, args)
}

func TestJVMCommandRunsGradleWithoutAPOM(t *testing.T) {
	t.Cleanup(func() {
		viper.Reset()
		settings.Init()
	})
	viper.Reset()
	settings.Init()
	t.Chdir(t.TempDir())
	jvm := NewJVM("junit5", nil)
	require.NoError(t, os.WriteFile("settings.gradle.kts", []byte("include(\"app\")\n"), 0644))
	command, args := jvm.Command()
	require.Equal(t, "gradle", command)
	require.Equal(t, []string{"test"}, args)
	require.NoError(t, os.WriteFile("gradlew", []byte("#!/bin/sh\n"), 0755))
	command, _ = jvm.Command()
	require.Equal(t, "./gradlew", command, "the Gradle wrapper, as CI usually runs it")
	require.NoError(t, os.WriteFile("pom.xml", []byte("<project/>\n"), 0644))
	command, _ = jvm.Command()
	require.Equal(t, "mvn", command, "a POM keeps Maven")
}

func TestJVMRejectsPlanAndRun(t *testing.T) {
	maven := NewJVM("junit4", nil)
	require.Equal(t, "junit4", maven.Name())
	require.Nil(t, maven.Platform())
	require.False(t, maven.SupportsFullTestDiscovery())
	_, err := maven.DiscoverTestFiles(context.Background(), discovery.TestFileSet{})
	require.ErrorIs(t, err, ErrJVMPlanRun)
	_, err = maven.DiscoverTests(context.Background(), discovery.TestFileSet{})
	require.ErrorIs(t, err, ErrJVMPlanRun)
	require.ErrorIs(t, maven.RunTests(context.Background(), nil, nil), ErrJVMPlanRun)
}
