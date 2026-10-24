package testdrive

import (
	"fmt"
	"maps"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
)

// Override only a simple action-owned local application. Its startup command
// and API execution path are unchanged; the override is recorded, never written
// to CI or substituted for an unvalidated configuration.
func applyPortFact(root string, entry *onboard.TestCommand, fact onboard.PortOverrideFact) error {
	return configurePortFact(root, entry, fact, false)
}

func configurePortFact(root string, entry *onboard.TestCommand, fact onboard.PortOverrideFact, allocate bool) error {
	plan := entry.Cypress
	if plan == nil || len(plan.Start) != 1 || len(plan.WaitOn) != 1 || len(plan.CLI) != 0 {
		return fmt.Errorf("port_override requires one action-owned Cypress application and readiness URL")
	}
	address, err := url.Parse(plan.WaitOn[0])
	if err != nil || address.Port() == "" || address.Scheme != "http" || address.User != nil || (address.Path != "" && address.Path != "/") {
		return fmt.Errorf("port_override requires a local HTTP root readiness URL with an explicit port")
	}
	ip := net.ParseIP(address.Hostname())
	if address.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("port_override cannot change a remote service")
	}
	if err := validateServiceFact(root, onboard.ServiceCommand{Argv: plan.Start[0], Directory: entry.Directory, ReadinessURL: "http://127.0.0.1:" + address.Port(), TimeoutSeconds: 60, PortEnvironment: fact.Environment}); err != nil {
		return err
	}
	if fact.Environment == "" || !filepath.IsLocal(fact.SourceFile) || strings.HasPrefix(filepath.ToSlash(fact.SourceFile), "node_modules/") || strings.HasPrefix(filepath.ToSlash(fact.SourceFile), ".git/") {
		return fmt.Errorf("port_override needs an existing application port environment variable and repository-local source_file")
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, fact.SourceFile))
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(canonicalWorkflowRoot(root), path)
	if err != nil || !filepath.IsLocal(relative) {
		return fmt.Errorf("port_override source_file escapes repository")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return fmt.Errorf("port_override source_file must be an application source file under 1 MiB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	declared := regexp.MustCompile(`process\.env\.`+regexp.QuoteMeta(fact.Environment)+`(?:[^A-Za-z0-9_$]|$)`).Match(data) || strings.Contains(string(data), "process.env['"+fact.Environment+"']") || strings.Contains(string(data), "process.env[\""+fact.Environment+"\"]")
	if !declared {
		return fmt.Errorf("%s does not declare process.env.%s; add the default-preserving application override, rediscover the current fingerprint, then supply facts", fact.SourceFile, fact.Environment)
	}
	config := map[string]any{}
	if existing := plan.Options["config"]; existing != nil {
		values, ok := existing.(map[string]any)
		if !ok {
			return fmt.Errorf("port_override cannot replace a custom Cypress config string; preserve and review its original options")
		}
		maps.Copy(config, values)
	}

	if existing, ok := config["baseUrl"].(string); ok && existing != "" {
		base, err := url.Parse(existing)
		if err != nil || base.Hostname() != address.Hostname() && base.Hostname() != "localhost" {
			return fmt.Errorf("port_override cannot replace a different configured application host")
		}
	}
	if !allocate {
		copyFact := fact
		entry.PortOverride = &copyFact
		return nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	if err := listener.Close(); err != nil {
		return err
	}
	address.Host = net.JoinHostPort("127.0.0.1", port)
	if existing, ok := config["baseUrl"].(string); ok && existing != "" {
		base, err := url.Parse(existing)
		if err != nil || base.Hostname() != address.Hostname() && base.Hostname() != "localhost" {
			return fmt.Errorf("port_override cannot replace a different configured application host")
		}
		base.Host = address.Host
		config["baseUrl"] = base.String()
	} else {
		config["baseUrl"] = address.String()
	}
	copyPlan := *plan
	copyPlan.Options = maps.Clone(plan.Options)
	if copyPlan.Options == nil {
		copyPlan.Options = map[string]any{}
	}
	copyPlan.Options["config"] = config
	copyPlan.WaitOn = []string{address.String()}
	entry.Cypress = &copyPlan
	entry.Environment = maps.Clone(entry.Environment)
	if entry.Environment == nil {
		entry.Environment = map[string]string{}
	}
	entry.Environment[fact.Environment] = port
	entry.LocalOverrides = append(entry.LocalOverrides, "Agent-declared existing "+fact.Environment+" in "+fact.SourceFile+"; local startup, Cypress baseUrl and readiness use "+address.String()+"; CI defaults unchanged.")
	return nil
}
