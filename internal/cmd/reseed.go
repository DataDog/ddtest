package cmd

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/DataDog/ddtest/internal/reseed"
	"github.com/spf13/cobra"
)

var reseedCmd = newReseedCommand()

// reseedKernel is replaced in tests, which can't reseed the kernel.
var reseedKernel = reseed.Reseed

func newReseedCommand() *cobra.Command {
	var seedFile string
	command := &cobra.Command{
		Use:   "reseed --seed-file path",
		Short: "Reseed the Linux kernel's random number generator",
		Long: fmt.Sprintf(`Reseed the Linux kernel's random number generator from a seed file.

Machines restored from the same memory snapshot start with the same kernel
random state, so until the kernel reseeds on its own they produce the same
"random" values, such as UUIDs or trace IDs. Run ddtest reseed as root on each
restored machine, with a different seed on each one, before running tests:

  sudo ddtest reseed --seed-file /tmp/seed

The seed file holds %d to %d bytes. ddtest mixes them into the kernel's
entropy pool, makes the kernel reseed at once, and deletes the file, so a
seed is never used twice. Linux only.`, reseed.MinSeedSize, reseed.MaxSeedSize),
		Args: cobra.NoArgs,
		// Reseeding needs no git checkout, so skip the root command's check.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			seed, err := os.ReadFile(seedFile)
			if err != nil {
				return fmt.Errorf("read the seed file: %w", err)
			}
			reseedErr := reseedKernel(seed)
			if err := os.Remove(seedFile); err != nil {
				reseedErr = errors.Join(reseedErr, fmt.Errorf("delete the seed file: %w", err))
			}
			if reseedErr != nil {
				return reseedErr
			}
			slog.Info("Reseeded the kernel random number generator", "seed_bytes", len(seed))
			return nil
		},
	}
	command.Flags().StringVar(&seedFile, "seed-file", "", "File with the seed; deleted after use")
	_ = command.MarkFlagRequired("seed-file")
	return command
}
