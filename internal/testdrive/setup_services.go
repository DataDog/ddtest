package testdrive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
)

const maxServiceReadinessSeconds = 300

func validateServiceFact(root string, service onboard.ServiceCommand) error {
	directory, err := localSetupDirectory(root, service.Directory)
	if err != nil {
		return err
	}
	if err := validatePrerequisiteFact(root, directory, PrerequisiteFact{Argv: serviceScriptArgv(service.Argv), ExpectedFiles: []string{"service-readiness"}}); err != nil {
		return err
	}
	address, err := url.Parse(service.ReadinessURL)
	if err != nil || address.Scheme != "http" || address.User != nil || address.Fragment != "" || address.Port() == "" {
		return fmt.Errorf("readiness_url must be an HTTP loopback URL with an explicit port")
	}
	ip := net.ParseIP(address.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("readiness_url must use a loopback IP, not a remote service or hostname")
	}
	if service.TimeoutSeconds < 1 || service.TimeoutSeconds > maxServiceReadinessSeconds {
		return fmt.Errorf("service timeout_seconds must be between 1 and 300")
	}
	if key := service.PortEnvironment; key != "" {
		if key != "PORT" && !strings.HasSuffix(key, "_PORT") {
			return fmt.Errorf("port_environment must name an existing PORT or *_PORT override")
		}
		for _, c := range key {
			if c != '_' && (c < 'A' || c > 'Z') {
				return fmt.Errorf("invalid port_environment")
			}
		}
		if strings.HasPrefix(key, "DD_") {
			return fmt.Errorf("port_environment cannot change instrumentation")
		}
	}
	return nil
}

type serviceExit struct {
	diagnostic string
	err        error
}

type serviceExecution struct {
	Argv         []string `json:"argv"`
	Directory    string   `json:"directory"`
	ReadinessURL string   `json:"readiness_url"`
	Ready        bool     `json:"ready"`
}

// Services are fresh for every configuration execution, never reused from an
// old checkpoint. Cancellation kills owned process groups through the executor.
func (t *Testdrive) startSetupServices(ctx context.Context, output io.Writer, item *configurationResult) (context.Context, func() error, error) {
	serviceCtx, cancel := context.WithCancelCause(ctx)
	var exits []chan serviceExit
	var cleanups []func() error
	stop := func() error {
		cancel(context.Canceled)
		var failures []error
		for _, cleanup := range cleanups {
			failures = append(failures, cleanup())
		}
		for _, exit := range exits {
			select {
			case result := <-exit:
				if result.err != nil && !errors.Is(result.err, context.Canceled) && !serviceCanceledBySignal(result.err) {
					failures = append(failures, fmt.Errorf("application service exited: %s: %w", result.diagnostic, result.err))
				}
			case <-time.After(10 * time.Second):
				failures = append(failures, fmt.Errorf("owned application service did not stop"))
			}
		}
		return errors.Join(failures...)
	}
	for _, service := range item.Services {
		if err := validateServiceFact(t.repositoryRoot, service); err != nil {
			return serviceCtx, stop, err
		}
		address, _ := url.Parse(service.ReadinessURL)
		listener, err := net.Listen("tcp", address.Host)
		if err != nil && service.PortEnvironment == "" {
			return serviceCtx, stop, fmt.Errorf("service port unavailable at %s; preserve the existing process and supply a documented port_environment override: %w", address.Host, err)
		}
		if service.PortEnvironment != "" {
			if err != nil {
				listener, err = net.Listen("tcp", net.JoinHostPort(address.Hostname(), "0"))
			}
			if err != nil {
				return serviceCtx, stop, err
			}
			port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
			address.Host = net.JoinHostPort(address.Hostname(), port)
			if item.Environment == nil {
				item.Environment = map[string]string{}
			} else {
				item.Environment = maps.Clone(item.Environment)
			}
			item.Environment[service.PortEnvironment] = port
		}
		if err := listener.Close(); err != nil {
			return serviceCtx, stop, err
		}
		item.ServiceExecutions = append(item.ServiceExecutions, serviceExecution{Argv: service.Argv, Directory: service.Directory, ReadinessURL: address.String()})
		env := maps.Clone(item.Environment)
		if env == nil {
			env = map[string]string{}
		}
		nodeOptions, ok := env["NODE_OPTIONS"]
		if !ok {
			nodeOptions = os.Getenv("NODE_OPTIONS")
		}
		env["NODE_OPTIONS"] = stripDatadogNodeOptions(nodeOptions)
		env["DD_TRACE_ENABLED"], env["DD_CIVISIBILITY_ENABLED"] = "false", "false"
		env["PATH"] = filepath.Join(t.repositoryRoot, "node_modules", ".bin") + string(os.PathListSeparator) + os.Getenv("PATH")
		executable, args, cleanup, err := t.ownSetupService(env, service.Argv)
		if err != nil {
			return serviceCtx, stop, err
		}
		cleanups = append(cleanups, cleanup)
		exit := make(chan serviceExit, 1)
		exits = append(exits, exit)
		_, _ = fmt.Fprintf(output, "Starting application service: %v; readiness %s\n", service.Argv, address)
		go func() {
			directory, err := localSetupDirectory(t.repositoryRoot, service.Directory)
			var data []byte
			if err == nil {
				if executor, ok := t.executor.(serviceOutputExecutor); ok {
					data, err = executor.ServiceOutput(serviceCtx, directory, executable, args, env)
				} else {
					data, err = setupOutput(serviceCtx, t.executor, t.repositoryRoot, service.Directory, executable, args, env)
				}
			}
			exit <- serviceExit{commandDiagnostic(data), err}
			if serviceCtx.Err() == nil {
				cancel(fmt.Errorf("application service exited before validation completed: %s: %v", commandDiagnostic(data), err))
			}
		}()
		readyCtx, readyCancel := context.WithTimeout(serviceCtx, time.Duration(service.TimeoutSeconds)*time.Second)
		err = waitForService(readyCtx, address.String())
		readyCancel()
		if err != nil {
			return serviceCtx, stop, fmt.Errorf("application readiness failed for %s: %w", address, errors.Join(err, context.Cause(serviceCtx)))
		}
		item.ServiceExecutions[len(item.ServiceExecutions)-1].Ready = true
	}
	return serviceCtx, stop, nil
}

func waitForService(ctx context.Context, address string) error {
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

type serviceOutputExecutor interface {
	ServiceOutput(context.Context, string, string, []string, map[string]string) ([]byte, error)
}

func (e configurationExecutor) ServiceOutput(ctx context.Context, directory, name string, args []string, env map[string]string) ([]byte, error) {
	executor, ok := e.commandExecutor.(serviceOutputExecutor)
	if !ok {
		return nil, fmt.Errorf("command executor cannot capture bounded service output")
	}
	return executor.ServiceOutput(ctx, directory, name, args, mergeConfigurationEnvironment(e.environment, env))
}
func serviceCanceledBySignal(err error) bool {
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ProcessState == nil {
		return false
	}
	status, ok := exit.Sys().(interface{ Signaled() bool })
	return ok && status.Signaled()
}

// pnpm built-ins that `pnpm <name>` runs instead of a package script.
var pnpmBuiltins = []string{"add", "audit", "bin", "config", "create", "deploy", "dlx", "env", "exec", "fetch", "i", "import", "init", "install", "link", "list", "ls", "outdated", "pack", "patch", "prune", "publish", "rebuild", "remove", "rm", "root", "server", "setup", "store", "unlink", "up", "update", "why"}

// serviceScriptArgv spells a package-manager shorthand (`npm start`,
// `yarn <script>`, `pnpm <script>`) as the `<manager> run <script>` it runs.
func serviceScriptArgv(words []string) []string {
	if len(words) == 2 && words[0] == "npm" && words[1] == "start" {
		return []string{"npm", "run", "start"}
	}
	if len(words) == 2 && words[0] == "yarn" && !strings.HasPrefix(words[1], "-") {
		return []string{"yarn", "run", words[1]}
	}
	if len(words) == 2 && words[0] == "pnpm" && !strings.HasPrefix(words[1], "-") && words[1] != "run" && !slices.Contains(pnpmBuiltins, words[1]) {
		return []string{"pnpm", "run", words[1]}
	}
	return words
}
