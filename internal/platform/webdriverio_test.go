package platform

import (
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestWebdriverIODetectionAndAliases(t *testing.T) {
	for _, hint := range []string{"", "webdriverio", "wdio"} {
		t.Run(hint, func(t *testing.T) {
			viper.Reset()
			viper.Set("framework", hint)
			settings.Init()
			t.Cleanup(func() { viper.Reset(); settings.Init() })
			root := t.TempDir()
			t.Chdir(root)
			require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"devDependencies":{"@wdio/cli":"9.20.1","@wdio/mocha-framework":"9.20.1"}}`), 0600))
			fw, err := NewJavaScript().DetectFramework()
			require.NoError(t, err)
			require.Equal(t, "webdriverio", fw.Name())
			require.False(t, fw.SupportsFullTestDiscovery())
		})
	}
}

func TestWebdriverIODoesNotChangeExistingMixedRepositoryDefault(t *testing.T) {
	for _, dependency := range []string{"jest", "mocha", "cypress", "@playwright/test", "vitest"} {
		t.Run(dependency, func(t *testing.T) {
			viper.Reset()
			settings.Init()
			t.Cleanup(func() { viper.Reset(); settings.Init() })
			root := t.TempDir()
			t.Chdir(root)
			body := `{"devDependencies":{"@wdio/cli":"9.20.0","` + dependency + `":"1.0.0"}}`
			require.NoError(t, os.WriteFile("package.json", []byte(body), 0600))
			runner, err := NewJavaScript().DetectFramework()
			require.NoError(t, err)
			expected := dependency
			if expected == "@playwright/test" {
				expected = "playwright"
			}
			require.Equal(t, expected, runner.Name())
		})
	}
}
