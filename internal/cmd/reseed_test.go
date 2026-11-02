package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/git"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runReseedCommand(t *testing.T, args ...string) error {
	t.Helper()
	command := newReseedCommand()
	command.SetArgs(args)
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	return command.Execute()
}

func useFakeReseed(t *testing.T, fake func([]byte) error) {
	t.Helper()
	original := reseedKernel
	reseedKernel = fake
	t.Cleanup(func() { reseedKernel = original })
}

func TestReseedCommandReseedsWithTheFileAndDeletesIt(t *testing.T) {
	seed := bytes.Repeat([]byte{7}, 32)
	seedFile := filepath.Join(t.TempDir(), "seed")
	require.NoError(t, os.WriteFile(seedFile, seed, 0o600))
	var got []byte
	useFakeReseed(t, func(s []byte) error {
		got = s
		return nil
	})

	require.NoError(t, runReseedCommand(t, "--seed-file", seedFile))

	assert.Equal(t, seed, got)
	assert.NoFileExists(t, seedFile)
}

func TestReseedCommandDeletesTheSeedEvenWhenReseedingFails(t *testing.T) {
	seedFile := filepath.Join(t.TempDir(), "seed")
	require.NoError(t, os.WriteFile(seedFile, make([]byte, 32), 0o600))
	useFakeReseed(t, func([]byte) error { return errors.New("operation not permitted") })

	err := runReseedCommand(t, "--seed-file", seedFile)

	require.ErrorContains(t, err, "operation not permitted")
	assert.NoFileExists(t, seedFile)
}

func TestReseedCommandNeedsASeedFile(t *testing.T) {
	useFakeReseed(t, func([]byte) error {
		t.Fatal("reseeded without a seed file")
		return nil
	})

	require.ErrorContains(t, runReseedCommand(t), `"seed-file" not set`)
	require.ErrorContains(t, runReseedCommand(t, "--seed-file", filepath.Join(t.TempDir(), "missing")), "read the seed file")
}

// The root command checks that git is available before any command runs;
// reseed runs on freshly restored machines and needs no checkout.
func TestReseedCommandRunsWithoutGit(t *testing.T) {
	originalLookPathFunc := git.LookPathFunc
	git.LookPathFunc = func(string) (string, error) { return "", errors.New("missing git") }
	t.Cleanup(func() { git.LookPathFunc = originalLookPathFunc })
	useFakeReseed(t, func([]byte) error { return nil })
	seedFile := filepath.Join(t.TempDir(), "seed")
	require.NoError(t, os.WriteFile(seedFile, make([]byte, 32), 0o600))
	root := &cobra.Command{Use: "ddtest", PersistentPreRunE: rootCmd.PersistentPreRunE}
	root.AddCommand(newReseedCommand())
	root.SetArgs([]string{"reseed", "--seed-file", seedFile})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})

	assert.NoError(t, root.Execute())
}

func TestReseedCommandIsRegistered(t *testing.T) {
	found := false
	for _, command := range rootCmd.Commands() {
		found = found || command == reseedCmd
	}
	assert.True(t, found, "reseed should be added to the root command")
}
