package testdrive

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/kballard/go-shellquote"
)

func workflowPackageManager(root string) string {
	var pkg struct {
		PackageManager string `json:"packageManager"`
	}
	data, _ := os.ReadFile(filepath.Join(root, "package.json"))
	if json.Unmarshal(data, &pkg) == nil && pkg.PackageManager != "" {
		return pkg.PackageManager
	}
	for _, candidate := range []struct{ File, Name string }{{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"package-lock.json", "npm"}} {
		if _, err := os.Stat(filepath.Join(root, candidate.File)); err == nil {
			return candidate.Name
		}
	}
	return "unresolved"
}

func workflowInitialization(framework string) string {
	switch framework {
	case "cypress":
		return "Preserve setupNodeEvents and support hooks; initialize dd-trace/ci/cypress in both."
	case "webdriverio":
		return "Initialize dd-trace/ci/init in the WDIO worker before framework imports; preserve loaders."
	case "cucumber":
		return "Initialize dd-trace/ci/init before Cucumber support imports; preserve NYC/ESM coverage."
	default:
		return "Initialize dd-trace/ci/init through NODE_OPTIONS before framework imports; preserve existing loaders."
	}
}

func setupFactsTemplate(fingerprint string, entry onboard.TestCommand) *onboard.SetupFacts {
	fact := onboard.ConfigurationFact{ConfigurationID: entry.ID, Directory: entry.Directory}
	if len(entry.Services) > 0 {
		fact.Services = append([]onboard.ServiceCommand{}, entry.Services...)
	} else {
		fact.Prerequisites = []onboard.PrerequisiteFact{{Argv: []string{}, ExpectedFiles: []string{}}}
	}
	return &onboard.SetupFacts{SchemaVersion: 2, Fingerprint: fingerprint, Configurations: []onboard.ConfigurationFact{fact}}
}

// Only explicit setup failures get repair instructions. Feature differences
// remain validation failures, never an invitation to alter their assertions.
func workflowSetupFailure(result validationResult) *onboard.TestCommand {
	for _, item := range result.Configurations {
		if !item.ExecutionAttempted && (len(item.Preparation) > 0 || len(item.Services) > 0 || strings.Contains(item.Error, "preparation") || strings.Contains(item.Error, "cypress startup port")) && item.Status != "passed" {
			entry := item.TestCommand
			return &entry
		}
	}
	return nil
}

func (t *Testdrive) preflightInDirectory(ctx context.Context, directory string, result *validationResult) (runErr error) {
	previous, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(directory); err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, os.Chdir(previous)) }()
	return t.preflight(ctx, io.Discard, result)
}

func cypressPortFactsTemplate(root, fingerprint string, entry onboard.TestCommand) *onboard.SetupFacts {
	sourceFile := ""
	words := entry.Cypress.Start[0]
	if len(words) == 3 && words[1] == "run" {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		data, _ := os.ReadFile(filepath.Join(root, entry.Directory, "package.json"))
		if json.Unmarshal(data, &pkg) == nil {
			words, _ = shellquote.Split(pkg.Scripts[words[2]])
		}
	}
	if len(words) >= 2 && words[0] == "node" && filepath.IsLocal(words[1]) && !strings.HasPrefix(words[1], "-") {
		sourceFile = filepath.ToSlash(filepath.Join(entry.Directory, words[1]))
	}
	return &onboard.SetupFacts{SchemaVersion: 2, Fingerprint: fingerprint, Configurations: []onboard.ConfigurationFact{{ConfigurationID: entry.ID, Directory: entry.Directory, PortOverride: &onboard.PortOverrideFact{Environment: "PORT", SourceFile: sourceFile}}}}
}
