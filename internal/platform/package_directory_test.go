package platform

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/settings"
	"github.com/stretchr/testify/require"
)

func usePackageDirectory(t *testing.T, directory string) {
	t.Helper()
	UsePackageDirectory(directory)
	t.Cleanup(func() { UsePackageDirectory("") })
}

func TestDetectionReadsTheSelectedJavaScriptPackage(t *testing.T) {
	resetDetectionSettings(t)
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "test", "package.json"), []byte(`{"dependencies":{"mocha":"11"}}`), 0644))
	// The repository root may hold another platform's manifest of its own.
	require.NoError(t, os.WriteFile(filepath.Join(root, "Gemfile"), []byte("gem 'rspec'\n"), 0644))
	t.Setenv("PATH", t.TempDir())

	p, err := DetectPlatform()
	require.NoError(t, err)
	require.Equal(t, "ruby", p.Name(), "without a selected package the root decides")

	usePackageDirectory(t, "test")
	require.Equal(t, filepath.Join("test"), PackageDirectory())
	p, err = DetectPlatform()
	require.NoError(t, err)
	require.Equal(t, "javascript", p.Name())
	fw, err := p.DetectFramework()
	require.NoError(t, err)
	require.Equal(t, "mocha", fw.Name(), "the framework comes from the selected package's manifest")

	settings.Get().Framework = "rspec"
	p, err = DetectPlatform()
	require.NoError(t, err)
	require.Equal(t, "ruby", p.Name(), "an explicit framework of another platform still selects it")
}

func TestPackageDirectoryDefaultsToTheWorkingDirectory(t *testing.T) {
	usePackageDirectory(t, "")
	require.Equal(t, ".", PackageDirectory())
}

func TestUndetectedPlatformErrorCarriesTheDetectionHint(t *testing.T) {
	resetDetectionSettings(t)
	t.Chdir(t.TempDir())
	_, err := DetectPlatform()
	require.EqualError(t, err, "could not detect a supported platform; inspect package.json, Python project configuration, Gemfile, or pom.xml, or specify --platform")

	UseDetectionHint("JavaScript packages below it: test")
	t.Cleanup(func() { UseDetectionHint("") })
	_, err = DetectPlatform()
	require.EqualError(t, err, "could not detect a supported platform: JavaScript packages below it: test")
}

func TestUnsupportedPlatformNamesTheSupportedSpelling(t *testing.T) {
	resetDetectionSettings(t)
	for hint, want := range map[string]string{
		"node":   "unsupported platform: node; use --platform javascript (supported platforms: javascript, python, ruby, java)",
		"NodeJS": "unsupported platform: NodeJS; use --platform javascript (supported platforms: javascript, python, ruby, java)",
		"go":     "unsupported platform: go; supported platforms: javascript, python, ruby, java",
	} {
		settings.Get().Platform = hint
		_, err := DetectPlatform()
		require.EqualError(t, err, want)
	}
}

func TestHasProjectManifest(t *testing.T) {
	root := t.TempDir()
	found, err := HasProjectManifest(root)
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{}`), 0644))
	found, err = HasProjectManifest(root)
	require.NoError(t, err)
	require.True(t, found)
}
