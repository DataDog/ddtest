package platform

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestJavaDetectionAndFrameworkSelection(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.WriteFile("pom.xml", []byte(`<project><dependencies><dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter</artifactId></dependency></dependencies></project>`), 0600))
	p, err := DetectPlatform()
	require.NoError(t, err)
	require.Equal(t, "java", p.Name())
	f, err := p.DetectFramework()
	require.NoError(t, err)
	require.Equal(t, "junit5", f.Name())
	_, err = f.DiscoverTestFiles(context.Background(), discovery.TestFileSet{})
	require.ErrorContains(t, err, "plan/run are not supported")
	viper.Set("framework", "cucumber-java")
	p, err = DetectPlatform()
	require.NoError(t, err)
	require.Equal(t, "java", p.Name())
}

func TestJavaAgentVersionAndDownloadSelector(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dd-java-agent.jar")
	file, err := os.Create(path)
	require.NoError(t, err)
	writer := zip.NewWriter(file)
	manifest, err := writer.Create("META-INF/MANIFEST.MF")
	require.NoError(t, err)
	_, err = manifest.Write([]byte("Premain-Class: datadog.trace.bootstrap.Agent\r\nImplementation-Version: 1.67.1\r\n"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NoError(t, file.Close())
	version, err := JavaAgentVersion(path)
	require.NoError(t, err)
	require.Equal(t, "1.67.1", version)
	j := NewJava()
	_, args, err := j.TracerInstallCommand(TracerOptions{Version: "1.67.1", Directory: t.TempDir()})
	require.NoError(t, err)
	require.Contains(t, args[len(args)-1], "/1.67.1/dd-java-agent-1.67.1.jar")
	for _, selector := range []string{"git:main", "../bad", "1.0;echo unsafe"} {
		_, _, err := j.TracerInstallCommand(TracerOptions{Version: selector})
		require.Error(t, err)
	}
	require.NoError(t, os.WriteFile(path, []byte("not a JAR"), 0600))
	_, err = JavaAgentVersion(path)
	require.Error(t, err)
}
