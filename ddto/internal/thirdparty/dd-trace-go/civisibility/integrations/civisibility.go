// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integrations

import (
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/cidelivery"
	tracer "github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer"
	infra "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/bazel"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/envconfig"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations/logs"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	civisibilitynet "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/net"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/env"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry"
)

// ciVisibilityCloseAction defines an action to be executed when CI visibility is closing.
type ciVisibilityCloseAction func()

// ciVisibilityIdleConnectionCloser closes idle HTTP connections owned by a CI
// Visibility client implementation.
type ciVisibilityIdleConnectionCloser interface {
	CloseIdleConnections()
}

// ciVisibilitySignalHandler owns the SIGINT/SIGTERM goroutine registered by CI
// Visibility and lets normal test shutdown stop it before goleak runs.
type ciVisibilitySignalHandler struct {
	signals  chan os.Signal // receives process interrupt and termination signals
	stop     chan struct{}  // asks the handler goroutine to exit during normal shutdown
	done     chan struct{}  // closes when the handler goroutine exits
	stopOnce sync.Once      // guarantees stop is signaled at most once
	stopping atomic.Bool    // suppresses os.Exit when normal shutdown already started
}

var (
	// ciVisibilityInitializationOnce ensures we initialize the CI visibility tracer only once.
	ciVisibilityInitializationOnce sync.Once

	// ciVisibilitySignalHandlerMu synchronizes access to activeCIVisibilitySignalHandler.
	ciVisibilitySignalHandlerMu sync.Mutex

	// activeCIVisibilitySignalHandler contains the active process signal handler, if CI Visibility started one.
	activeCIVisibilitySignalHandler *ciVisibilitySignalHandler

	// ciVisibilitySignalExitFunc terminates the process after signal-triggered shutdown; tests replace it.
	ciVisibilitySignalExitFunc = os.Exit

	// closeActions holds CI visibility close actions.
	closeActions []ciVisibilityCloseAction
	// preCloseActions holds barriers that must finish before regular close actions run.
	preCloseActions []ciVisibilityCloseAction
	// ciVisibilityShutdownDone closes after the current shutdown owner finishes.
	ciVisibilityShutdownDone chan struct{}
	// waitForCIVisibilityShutdown blocks shutdown callers that do not own the
	// Initialized -> Exiting transition. It is a variable for deterministic tests.
	waitForCIVisibilityShutdown = func(done <-chan struct{}) { <-done }

	// closeActionsMutex synchronizes access to closeActions, preCloseActions, and
	// ciVisibilityShutdownDone.
	closeActionsMutex sync.Mutex
)

// EnsureCiVisibilityInitialization initializes the CI visibility tracer if it hasn't been initialized already.
func EnsureCiVisibilityInitialization() {
	internalCiVisibilityInitialization(func(opts []tracer.StartOption) {
		// Initialize the tracer.
		tracer.Start(opts...)
	})
}

// internalCiVisibilityInitialization runs the one-time CI Visibility bootstrap and wires the selected tracer initializer into it.
func internalCiVisibilityInitialization(tracerInitializer func([]tracer.StartOption)) {
	if IsProcessRetryChild() {
		return
	}
	ciVisibilityInitializationOnce.Do(func() {
		civisibility.SetState(civisibility.StateInitializing)
		defer civisibility.SetState(civisibility.StateInitialized)

		// check the debug flag to enable debug logs. The tracer initialization happens
		// after the CI Visibility initialization so we need to handle this flag ourselves
		if infra.BoolEnv("DD_TRACE_DEBUG", false) {
			log.SetLevel(log.LevelDebug)
		}

		log.Debug("civisibility: initializing")
		if log.DebugEnabled() {
			started := time.Now()
			defer func() {
				log.Debug("civisibility: runtime bootstrap finished duration=%s", time.Since(started).Round(time.Microsecond))
			}()
		}

		enabledMode, _ := envconfig.FromEnv()
		parentOnly := enabledMode == envconfig.EnabledModeParent

		// Since calling this method indicates we are in CI Visibility mode, set the environment variable.
		// Note: this runs right before tracer.Start below, so config.RecordProductStart's
		// env-diff telemetry can misattribute this self-mutation to a cross-product change
		// if another product's Start already ran in this process.
		_ = os.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")

		// Avoid sampling rate warning (in CI Visibility mode we send all data)
		_ = os.Setenv("DD_TRACE_SAMPLE_RATE", "1")

		// Preload the CodeOwner file
		_ = utils.GetCodeOwners()

		// Capabilities are static and must be available even before the async
		// settings request completes. Manual spans may start immediately.
		utils.AddCITagsMap(libraryCapabilities())

		// Preload all CI, Git, and CodeOwners tags.
		ciTags := utils.GetCITagsReadOnly()
		_ = utils.GetCIMetrics()

		// Check if DD_SERVICE has been set; otherwise default to the repo name (from the spec).
		var opts []tracer.StartOption
		serviceName := env.Get("DD_SERVICE")
		if serviceName == "" {
			serviceName = utils.ServiceFromCodeOwners()
			if serviceName != "" {
				opts = append(opts, tracer.WithService(serviceName))
			}
		}
		if serviceName == "" {
			if repoURL, ok := ciTags[constants.GitRepositoryURL]; ok {
				// regex to sanitize the repository url to be used as a service name
				repoRegex := regexp.MustCompile(`(?m)/([a-zA-Z0-9\-_.]*)$`)
				matches := repoRegex.FindStringSubmatch(repoURL)
				if len(matches) > 1 {
					repoURL = strings.TrimSuffix(matches[1], ".git")
				}
				serviceName = repoURL
				opts = append(opts, tracer.WithService(serviceName))
			}
		}

		// Initializing additional features asynchronously
		if cidelivery.Enabled() {
			ensureAdditionalFeaturesInitialization(serviceName)
		} else {
			done := cidelivery.TrackBackground() // A goleak check waits for it.
			go func() {
				defer done()
				ensureAdditionalFeaturesInitialization(serviceName)
			}()
		}

		// Initialize the tracer
		log.Debug("civisibility: initializing tracer")
		tracerInitializer(opts)

		if parentOnly {
			_ = os.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "false")
		}

		initializeCiVisibilityLogs(serviceName)

		startCIVisibilitySignalHandler()
	})
}

// run waits for either a process signal or a normal shutdown request.
func (handler *ciVisibilitySignalHandler) run() {
	defer close(handler.done)

	select {
	case <-handler.signals:
		if handler.stopping.Load() {
			return
		}
		exitCiVisibility(false)
		if handler.stopping.Load() {
			return
		}
		ciVisibilitySignalExitFunc(1)
	case <-handler.stop:
		return
	}
}

// startCIVisibilitySignalHandler registers the process signal handler once for
// the current CI Visibility initialization.
func startCIVisibilitySignalHandler() {
	ciVisibilitySignalHandlerMu.Lock()
	defer ciVisibilitySignalHandlerMu.Unlock()

	if activeCIVisibilitySignalHandler != nil {
		return
	}

	handler := &ciVisibilitySignalHandler{
		signals: make(chan os.Signal, 1),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}

	activeCIVisibilitySignalHandler = handler
	signal.Notify(handler.signals, syscall.SIGINT, syscall.SIGTERM)
	go handler.run()
}

// markCIVisibilitySignalHandlerStopping prevents a late buffered signal from
// converting an already-started normal shutdown into os.Exit(1).
func markCIVisibilitySignalHandlerStopping() {
	ciVisibilitySignalHandlerMu.Lock()
	handler := activeCIVisibilitySignalHandler
	ciVisibilitySignalHandlerMu.Unlock()

	if handler != nil {
		handler.stopping.Store(true)
	}
}

// stopCIVisibilitySignalHandler stops the active signal handler and waits for
// its goroutine to exit. It is safe to call repeatedly.
func stopCIVisibilitySignalHandler() {
	ciVisibilitySignalHandlerMu.Lock()
	handler := activeCIVisibilitySignalHandler
	ciVisibilitySignalHandlerMu.Unlock()

	if handler == nil {
		return
	}

	handler.stopOnce.Do(func() {
		handler.stopping.Store(true)
		signal.Stop(handler.signals)
		close(handler.stop)
	})

	<-handler.done

	ciVisibilitySignalHandlerMu.Lock()
	if activeCIVisibilitySignalHandler == handler {
		activeCIVisibilitySignalHandler = nil
	}
	ciVisibilitySignalHandlerMu.Unlock()
}

// initializeCiVisibilityLogs starts CI Visibility log shipping only when logs are enabled and Bazel offline/file modes do not suppress it.
func initializeCiVisibilityLogs(serviceName string) {
	if !shouldInitializeCiVisibilityLogs(logs.IsEnabled()) {
		if bazel.IsManifestModeEnabled() || bazel.IsPayloadFilesModeEnabled() {
			log.Debug("civisibility: logs initialization skipped for test optimization offline/file mode")
			return
		}
		log.Debug("civisibility: logs are disabled")
		return
	}

	log.Debug("civisibility: initializing logs for service: %s", serviceName)
	logs.Initialize(serviceName)
}

// shouldInitializeCiVisibilityLogs reports whether CI Visibility log collection should start for the current process mode.
func shouldInitializeCiVisibilityLogs(logsEnabled bool) bool {
	if bazel.IsManifestModeEnabled() || bazel.IsPayloadFilesModeEnabled() {
		return false
	}
	return logsEnabled
}

// PushCiVisibilityCloseAction adds a close action to be executed when CI visibility exits.
func PushCiVisibilityCloseAction(action ciVisibilityCloseAction) {
	closeActionsMutex.Lock()
	defer closeActionsMutex.Unlock()
	// Append in registration order; shutdown walks backwards to preserve LIFO.
	closeActions = append(closeActions, action)
}

// TryPushCiVisibilityPreCloseAction adds a barrier that runs before regular
// close actions, without holding closeActionsMutex while the barrier executes.
func TryPushCiVisibilityPreCloseAction(action ciVisibilityCloseAction) bool {
	closeActionsMutex.Lock()
	defer closeActionsMutex.Unlock()
	state := civisibility.GetState()
	if state == civisibility.StateExiting || state == civisibility.StateExited {
		return false
	}
	preCloseActions = append(preCloseActions, action)
	return true
}

// ExitCiVisibility executes all registered close actions and stops the tracer.
func ExitCiVisibility() {
	if IsProcessRetryChild() {
		return
	}
	markCIVisibilitySignalHandlerStopping()
	exitCiVisibility(true)
}

// exitCiVisibility executes CI Visibility shutdown and optionally stops the
// signal handler. Signal-triggered shutdown skips that wait to avoid self-deadlock.
func exitCiVisibility(stopSignalHandler bool) {
	debug := log.DebugEnabled()
	owner := false
	if debug {
		started := time.Now()
		// Registered before shutdown defers so state publication, unlocks and
		// the signal-handler join all belong to the complete duration.
		defer func() {
			log.Debug("civisibility: shutdown finished duration=%s owner=%t", time.Since(started), owner)
		}()
	}
	closeActionsMutex.Lock()
	if civisibility.GetState() != civisibility.StateInitialized {
		done := ciVisibilityShutdownDone
		closeActionsMutex.Unlock()
		log.Debug("civisibility: already closed or not initialized")
		if done != nil {
			waitForCIVisibilityShutdown(done)
		}
		if stopSignalHandler {
			stopCIVisibilitySignalHandler()
		}
		return
	}
	owner = true
	civisibility.SetState(civisibility.StateExiting)

	done := make(chan struct{})
	ciVisibilityShutdownDone = done
	barriers := append([]ciVisibilityCloseAction(nil), preCloseActions...)
	preCloseActions = nil
	closeActionsMutex.Unlock()
	if stopSignalHandler {
		defer stopCIVisibilitySignalHandler()
	}
	defer func() {
		closeActionsMutex.Lock()
		civisibility.SetState(civisibility.StateExited)
		if ciVisibilityShutdownDone == done {
			close(done)
			ciVisibilityShutdownDone = nil
		}
		closeActionsMutex.Unlock()
	}()
	log.Debug("civisibility: exiting")
	var stageStarted time.Time
	if debug {
		stageStarted = time.Now()
	}
	if cidelivery.Enabled() {
		cidelivery.Shutdown()
	}
	for i := len(barriers) - 1; i >= 0; i-- {
		barriers[i]()
	}
	if debug {
		log.Debug("civisibility: shutdown barriers finished duration=%s actions=%d", time.Since(stageStarted), len(barriers))
	}

	closeActionsMutex.Lock()
	defer closeActionsMutex.Unlock()
	defer func() {
		closeActions = []ciVisibilityCloseAction{}
		preCloseActions = []ciVisibilityCloseAction{}
		log.Debug("civisibility: flushing and stopping the logger")
		if debug {
			stageStarted = time.Now()
		}
		logs.Stop()
		if debug {
			log.Debug("civisibility: logger stop finished duration=%s", time.Since(stageStarted))
		}
		log.Debug("civisibility: flushing and stopping tracer")
		// Stop closes the client, and Close performs the final flush. A separate
		// Flush first would only repeat a failed delivery's retries and timeout.
		tracer.Stop()
		if debug {
			stageStarted = time.Now()
		}
		telemetry.StopApp()
		closeCIVisibilityIdleConnections()
		if debug {
			log.Debug("civisibility: telemetry stop finished duration=%s", time.Since(stageStarted))
		}
		log.Debug("civisibility: done.")
	}()
	if debug {
		stageStarted = time.Now()
	}
	for i := len(closeActions) - 1; i >= 0; i-- {
		closeActions[i]()
	}
	if debug {
		log.Debug("civisibility: close actions finished duration=%s actions=%d", time.Since(stageStarted), len(closeActions))
	}
}

// closeCIVisibilityIdleConnections releases keep-alive connections after all CI
// Visibility components have completed their shutdown flushes.
func closeCIVisibilityIdleConnections() {
	if closer, ok := ciVisibilityClient.(ciVisibilityIdleConnectionCloser); ok {
		closer.CloseIdleConnections()
	}
	civisibilitynet.CloseIdleConnections()
}
