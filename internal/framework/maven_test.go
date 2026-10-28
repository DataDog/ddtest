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

func TestMavenCommandPrefersTheWrapper(t *testing.T) {
	t.Cleanup(func() {
		viper.Reset()
		settings.Init()
	})
	viper.Reset()
	settings.Init()
	t.Chdir(t.TempDir())
	maven := NewMaven("junit5", nil)
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

func TestMavenRejectsPlanAndRun(t *testing.T) {
	maven := NewMaven("junit4", nil)
	require.Equal(t, "junit4", maven.Name())
	require.Nil(t, maven.Platform())
	require.False(t, maven.SupportsFullTestDiscovery())
	_, err := maven.DiscoverTestFiles(context.Background(), discovery.TestFileSet{})
	require.ErrorIs(t, err, ErrMavenPlanRun)
	_, err = maven.DiscoverTests(context.Background(), discovery.TestFileSet{})
	require.ErrorIs(t, err, ErrMavenPlanRun)
	require.ErrorIs(t, maven.RunTests(context.Background(), nil, nil), ErrMavenPlanRun)
}
