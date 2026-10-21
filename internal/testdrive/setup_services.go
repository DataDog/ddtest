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
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
)

type directoryExecutor interface {
	CombinedOutputInDirectory(context.Context, string, string, []string, map[string]string) ([]byte, error)
}

func setupOutput(ctx context.Context, executor commandExecutor, root, relative, name string, args []string, env map[string]string) ([]byte, error) {
	directory, err := localSetupDirectory(root, relative)
	if err != nil {
		return nil, err
	}
	if directory == canonicalWorkflowRoot(root) && directory == canonicalWorkflowRoot(currentDirectory()) {
		return executor.CombinedOutput(ctx, name, args, env)
	}
	if e, ok := executor.(directoryExecutor); ok {
		return e.CombinedOutputInDirectory(ctx, directory, name, args, env)
	}
	return nil, fmt.Errorf("command executor cannot set setup working directory %s", relative)
}

func currentDirectory() string { directory, _ := os.Getwd(); return directory }

func validateServiceFact(root string, service onboard.ServiceCommand) error {
	directory, err := localSetupDirectory(root, service.Directory)
	if err != nil {
		return err
	}
	if err := validatePrerequisiteFact(root, directory, PrerequisiteFact{Argv: service.Argv, ExpectedFiles: []string{"service-readiness"}}); err != nil {
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
	if service.TimeoutSeconds < 1 || service.TimeoutSeconds > 300 {
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

// Preserve an existing listener rather than letting the action start a second
// application and spend a full test run discovering EADDRINUSE.
func checkCypressStartupPorts(plan *onboard.CypressCommand) error {
	if plan == nil || len(plan.Start) == 0 {
		return nil
	}
	for _, value := range plan.WaitOn {
		address, err := url.Parse(value)
		if err != nil || address.Port() == "" {
			continue
		}
		ip := net.ParseIP(address.Hostname())
		if address.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			continue
		}
		connection, err := net.DialTimeout("tcp", address.Host, 200*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return fmt.Errorf("cypress startup port %s is already occupied. Preserve that listener. Use a documented application/test port override consistently in the startup command, Cypress baseUrl and action wait-on URL; retain their CI defaults", address.Host)
		}
	}
	return nil
}

// Services are fresh for every configuration execution, never reused from an
// old checkpoint. Cancellation kills owned process groups through the executor.
func (t *Testdrive) startSetupServices(ctx context.Context, output io.Writer, item *configurationResult) (context.Context, func() error, error) {
	serviceCtx, cancel := context.WithCancelCause(ctx)
	var exits []chan serviceExit
	stop := func() error {
		cancel(context.Canceled)
		var failures []error
		for _, exit := range exits {
			select {
			case result := <-exit:
				if result.err != nil && !errors.Is(result.err, context.Canceled) && !strings.Contains(result.err.Error(), "signal: killed") {
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
		exit := make(chan serviceExit, 1)
		exits = append(exits, exit)
		_, _ = fmt.Fprintf(output, "Starting application service: %v; readiness %s\n", service.Argv, address)
		go func() {
			data, err := setupOutput(serviceCtx, t.executor, t.repositoryRoot, service.Directory, service.Argv[0], service.Argv[1:], env)
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
