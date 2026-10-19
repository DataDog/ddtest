package onboard

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestMavenOnboardingUsesJavaActionAndNativeValidation(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	require.NoError(t, os.WriteFile("pom.xml", []byte(`<project><dependencies><dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter</artifactId></dependency></dependencies></project>`), 0600))
	require.NoError(t, os.WriteFile(".github/workflows/tests.yml", []byte("jobs:\n  test:\n    steps:\n      - run: mvn -B -Pci verify\n"), 0600))
	var output bytes.Buffer
	require.NoError(t, Run(&output))
	for _, text := range []string{"DDTest found java, junit5", "languages: java", "MAVEN_OPTS", "--framework junit5", "plan/run", "Scala MUnit"} {
		require.Contains(t, output.String(), text)
	}
	require.NotContains(t, output.String(), "NODE_OPTIONS")
}
