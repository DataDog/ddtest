package platform

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/stretchr/testify/require"
)

func resetDetectionSettings(t *testing.T) {
	t.Helper()
	cfg := settings.Get()
	old := *cfg
	cfg.Platform = ""
	cfg.Framework = ""
	cfg.Command = ""
	t.Cleanup(func() { *cfg = old })
}

func TestAutomaticPlatformAndFrameworkSelection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake runtimes use POSIX shell")
	}
	for _, tc := range []struct{ name, file, contents, runtime, version, runner, env string }{
		{"javascript", "package.json", `{"devDependencies":{"jest":"29"}}`, "node", "v24.0.0", "jest", "NODE_OPTIONS"},
		{"python", "pytest.ini", "[pytest]\n", "python", "4.11.0", "pytest", "PYTEST_ADDOPTS"},
		{"ruby", "Gemfile", "gem 'rspec'\n", "bundle", "  * datadog-ci (1.31.0)", "rspec", "RUBYOPT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetDetectionSettings(t)
			root := t.TempDir()
			t.Chdir(root)
			require.NoError(t, os.WriteFile(tc.file, []byte(tc.contents), 0644))
			bin := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(bin, tc.runtime), []byte("#!/bin/sh\nprintf '%s\\n' '"+tc.version+"'\n"), 0755))
			t.Setenv("PATH", bin)
			p, err := DetectPlatform(context.Background())
			require.NoError(t, err)
			require.Equal(t, tc.name, p.Name())
			fw, err := p.DetectFramework()
			require.NoError(t, err)
			require.Equal(t, tc.runner, fw.Name())
			require.Contains(t, fw.GetPlatformEnv(), tc.env)
			lang, readOnly, err := DetectTestProject(root, "")
			require.NoError(t, err)
			require.Equal(t, p.Name(), lang)
			require.Equal(t, fw.Name(), readOnly.Name())
		})
	}
}

func TestExplicitSelectionOverridesProjectEvidence(t *testing.T) {
	resetDetectionSettings(t)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte("{"), 0644))
	settings.Get().Framework = "pytest"
	lang, fw, err := DetectTestProject(root, "")
	require.NoError(t, err)
	require.Equal(t, "python", lang)
	require.Equal(t, "pytest", fw.Name())
	settings.Get().Platform = "javascript"
	_, _, err = DetectTestProject(root, "")
	require.ErrorContains(t, err, "not supported by platform")
	settings.Get().Framework = "jest"
	lang, fw, err = DetectTestProject(root, "")
	require.NoError(t, err)
	require.Equal(t, "javascript", lang)
	require.Equal(t, "jest", fw.Name())
}

func TestPlatformHintResolvesPolyglotProject(t *testing.T) {
	resetDetectionSettings(t)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"devDependencies":{"jest":"29"}}`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "pytest.ini"), []byte("[pytest]\n"), 0644))
	_, _, err := DetectTestProject(root, "")
	require.ErrorContains(t, err, "multiple platforms")
	settings.Get().Platform = "python"
	lang, fw, err := DetectTestProject(root, "")
	require.NoError(t, err)
	require.Equal(t, "python", lang)
	require.Equal(t, "pytest", fw.Name())
}

func TestJavaScriptDetectionDoesNotOverrideCommand(t *testing.T) {
	for _, script := range []string{"jest --config custom.js", "vitest run", "jest && eslint .", "cross-env NODE_ENV=test jest"} {
		t.Run(script, func(t *testing.T) {
			resetDetectionSettings(t)
			root := t.TempDir()
			t.Chdir(root)
			require.NoError(t, os.WriteFile("package.json", []byte(`{"devDependencies":{"jest":"29"},"scripts":{"test":"`+script+`"}}`), 0644))
			for _, custom := range []string{"", "custom-jest --config explicit.js"} {
				settings.Get().Command = custom
				fw, err := NewJavaScript().detectFramework(root, "jest")
				require.NoError(t, err)
				command, args := fw.(*framework.Jest).TestCommand(nil)
				if custom == "" {
					require.Equal(t, "npx", command)
					require.Equal(t, []string{"jest"}, args)
				} else {
					require.Equal(t, "custom-jest", command)
					require.Equal(t, []string{"--config", "explicit.js"}, args)
				}
			}
		})
	}
}
