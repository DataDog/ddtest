package testdrive

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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
	if text := languageOrUnsupported(framework).initialization(framework); text != "" {
		return text
	}
	switch framework {
	case "cypress":
		return "For the Datadog CI action, set the Cypress step NODE_OPTIONS to -r ${{ env.DD_TRACE_PACKAGE }}, preserving existing options. Guard action installation and preload together when secrets are absent. Put the secret in a job-level env variable and test env.<name>; GitHub forbids secrets.* directly in if. Preserve defineConfig, setupNodeEvents and support hooks, and any existing Istanbul instrumentation that Test Impact Analysis reads from window.__coverage__. Without it, testdrive reports the build change for the user to approve; do not add it yourself."
	case "webdriverio":
		return "Initialize dd-trace/ci/init in the WDIO worker before framework imports; preserve loaders."
	case "cucumber":
		return "Initialize dd-trace/ci/init before Cucumber support imports; preserve NYC/ESM coverage."
	default:
		return "Initialize dd-trace/ci/init through NODE_OPTIONS before framework imports; preserve existing loaders."
	}
}

// pendingSetup names what this configuration still needs: unvalidated setup
// and the prerequisites that wait for agent-supplied expected outputs.
func pendingSetup(entry onboard.TestCommand) []string {
	pending := slices.Clone(entry.UnvalidatedSetup)
	for _, build := range entry.Prerequisites {
		if build.Kind == onboard.PreparationNeedsFacts && build.FactSource == "" {
			pending = append(pending, "prerequisite "+build.Command+" needs expected_files")
		}
	}
	return pending
}

func setupFactsTemplate(fingerprint string, entry onboard.TestCommand) *onboard.SetupFacts {
	fact := onboard.ConfigurationFact{ConfigurationID: entry.ID, Directory: entry.Directory}
	for _, build := range entry.Prerequisites {
		if build.Kind == onboard.PreparationNeedsFacts && build.FactSource == "" {
			words, _ := shellquote.Split(build.Command)
			fact.Prerequisites = append(fact.Prerequisites, onboard.PrerequisiteFact{ForCommand: entry.Command, Directory: build.Directory, Argv: words, ExpectedFiles: []string{}})
		}
	}
	if len(fact.Prerequisites) > 0 {
		return &onboard.SetupFacts{SchemaVersion: 2, Fingerprint: fingerprint, Configurations: []onboard.ConfigurationFact{fact}}
	}
	if len(entry.Services) > 0 {
		fact.Services = append([]onboard.ServiceCommand{}, entry.Services...)
	} else {
		fact.Prerequisites = []onboard.PrerequisiteFact{{ForCommand: entry.Command, Argv: []string{}, ExpectedFiles: []string{}}}
	}
	return &onboard.SetupFacts{SchemaVersion: 2, Fingerprint: fingerprint, Configurations: []onboard.ConfigurationFact{fact}}
}

// scopeFactsTemplate lists configurations eligible for instrumentation, so
// rejected facts can be rewritten against the current fingerprint and IDs.
func scopeFactsTemplate(fingerprint string, scope onboard.ValidationScope) *onboard.SetupFacts {
	template := &onboard.SetupFacts{SchemaVersion: 2, Fingerprint: fingerprint}
	for _, entry := range scope.Commands {
		if ciInstrumentationDisabled(entry) {
			continue
		}
		template.Configurations = append(template.Configurations, setupFactsTemplate(fingerprint, entry).Configurations...)
	}
	return template
}

// Only explicit setup failures get repair instructions. Feature differences
// remain validation failures, never an invitation to alter their assertions.
func workflowSetupFailure(result validationResult) (*onboard.TestCommand, string) {
	for _, item := range result.Configurations {
		if !item.ExecutionAttempted && (len(item.Preparation) > 0 || len(item.Services) > 0 || strings.Contains(item.Error, "preparation") || strings.Contains(item.Error, "cypress startup port")) && item.Status != "passed" {
			entry := item.TestCommand
			return &entry, setupFailureCause(item)
		}
	}
	return nil, ""
}

// setupFailureCause names why setup stopped: the run error (such as a failed
// readiness check), else the first prerequisite that didn't pass.
func setupFailureCause(item configurationResult) string {
	if item.Error != "" {
		return item.Error
	}
	for _, build := range item.Preparation {
		if build.Status == "passed" || build.Status == "not exercised" {
			continue
		}
		cause := build.Command + ": " + build.Status
		if detail := cmp.Or(build.Diagnostic, build.Code); detail != "" {
			cause += " (" + detail + ")"
		}
		return cause
	}
	return ""
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

func missingPrerequisiteFacts(entry onboard.TestCommand) bool {
	for _, build := range entry.Prerequisites {
		if build.Kind == onboard.PreparationNeedsFacts && build.FactSource == "" {
			return true
		}
	}
	return false
}
