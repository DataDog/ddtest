package testdrive

import (
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"testing"
)

func TestExplicitCommandExtractsLiteralEnvironment(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"devDependencies":{"jest":"29"}}`)
	viper.Reset()
	viper.Set("framework", "jest")
	viper.Set("command", `OUTPUT='two words' npx jest --ci`)
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	drive, err := Prepare(PrepareOptions{TracerVersion: "6.20.1", CheckOnly: true})
	require.NoError(t, err)
	require.NotContains(t, drive.command, "OUTPUT=")
	require.Equal(t, "two words", drive.configurationEnvironment["OUTPUT"])
	require.Equal(t, "two words", drive.preflightEnvironment["OUTPUT"])
}
