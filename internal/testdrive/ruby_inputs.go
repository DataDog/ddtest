package testdrive

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
)

// Bind receipts to the actual bundles and settings, including ignored inputs.
// Read only: no Gemfile or other repository Ruby is evaluated here. Configs may
// contain credentials; only their digest becomes part of the receipt.
func fingerprintRubyBundles(ctx context.Context, root string, hash io.Writer) error {
	commands := []onboard.TestCommand{{Directory: "."}}
	for _, name := range []string{"rspec", "minitest"} {
		// Unresolved discovery already blocks completion. Its source is still
		// fingerprinted; only statically selected consumers add bundle inputs.
		if scope, err := onboard.DiscoverValidationScope(root, name); err == nil {
			commands = append(commands, scope.Commands...)
		}
	}
	paths := map[string]bool{}
	for i, command := range commands {
		command.Environment = rubyBundleEnvironment(command)
		gemfile, err := rubyConfigurationGemfile(root, command.Directory, command.Environment)
		if err != nil {
			// An unrelated ambient bundle must not block non-Ruby repositories.
			if i == 0 {
				continue
			}
			return err
		}
		manifest := filepath.Join(root, gemfile)
		if i == 0 {
			if _, err := os.Stat(manifest); os.IsNotExist(err) {
				continue
			}
		}
		value := func(key string) string {
			if value, ok := command.Environment[key]; ok {
				return value
			}
			return os.Getenv(key)
		}
		lock := value("BUNDLE_LOCKFILE")
		if lock == "" {
			lock = filepath.Join(root, rubyLockfile(gemfile))
		} else if !filepath.IsAbs(lock) {
			lock = filepath.Join(root, command.Directory, lock)
		}
		paths[manifest], paths[lock] = true, true
		bundleRoot := filepath.Dir(manifest)
		appConfig := value("BUNDLE_APP_CONFIG")
		_, configured := command.Environment["BUNDLE_APP_CONFIG"]
		if _, ambient := os.LookupEnv("BUNDLE_APP_CONFIG"); !configured && !ambient {
			appConfig = ".bundle"
		}
		if !filepath.IsAbs(appConfig) {
			appConfig = filepath.Join(bundleRoot, appConfig)
		}
		paths[filepath.Join(appConfig, "config")] = true
		global := value("BUNDLE_CONFIG")
		if global == "" {
			global = value("BUNDLE_USER_CONFIG")
		}
		if global == "" {
			home := value("BUNDLE_USER_HOME")
			if home == "" {
				home = filepath.Join(value("HOME"), ".bundle")
			}
			global = filepath.Join(home, "config")
		}
		if !filepath.IsAbs(global) {
			global = filepath.Join(root, command.Directory, global)
		}
		paths[global] = true
	}
	if len(paths) == 0 {
		return nil
	}
	for _, item := range slices.Sorted(slices.Values(os.Environ())) {
		if strings.HasPrefix(item, "BUNDLE_") {
			_, _ = fmt.Fprintf(hash, "bundler-env\x00%s\x00", item)
		}
	}
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(hash, "ruby-input\x00%s\x00", path)
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			_, _ = io.WriteString(hash, "missing\x00")
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("ruby bundle input is not a regular file: %s", path)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, readErr := io.Copy(hash, file)
		closeErr := file.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		_, _ = io.WriteString(hash, "\x00")
	}
	return nil
}
