// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gradle

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadProperties(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gradle.properties", "# comment \\\n"+
		"! also a comment\n"+
		"  equals = one\n"+
		"colon:two\n"+
		"space   three\n"+
		"continued = a \\\n    b \\\r\n\tc\n"+
		"escaped\\ key\\:x = tab\\there \\u0041\\\\\n"+
		"empty\n"+
		"last = trailing\\")
	properties, err := ReadProperties(filepath.Join(root, "gradle.properties"))
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"equals":        "one",
		"colon":         "two",
		"space":         "three",
		"continued":     "a b c",
		"escaped key:x": "tab\there A\\",
		"empty":         "",
		"last":          "trailing",
	}, properties)
	_, err = ReadProperties(filepath.Join(root, "missing.properties"))
	require.Error(t, err)
}

func TestSystemPropertiesAndUserHomes(t *testing.T) {
	args := []string{"-Dorg.gradle.jvmargs=-Xmx1g", "test", "-D", "a=b", "--system-prop=c=d=e", "-g", "home", "--gradle-user-home=other", "-Dgradle.user.home=prop"}
	require.Equal(t, map[string]string{"org.gradle.jvmargs": "-Xmx1g", "a": "b", "c": "d=e", "gradle.user.home": "prop"}, SystemProperties(args))
	require.Equal(t, []string{"home", "other", "prop"}, UserHomes(args))
	require.Empty(t, UserHomes([]string{"test"}))
}
