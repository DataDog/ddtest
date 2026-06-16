// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integrations

import (
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/DataDog/ddtest/civisibility"
	"github.com/DataDog/ddtest/civisibility/constants"
	"github.com/DataDog/ddtest/internal/utils"
	"github.com/DataDog/ddtest/stableconfig"
)

// ciVisibilityCloseAction defines an action to be executed when CI visibility is closing.
type ciVisibilityCloseAction func()

var (
	// ciVisibilityInitializationOnce ensures we initialize CI visibility only once.
	ciVisibilityInitializationOnce sync.Once

	// closeActions holds CI visibility close actions.
	closeActions []ciVisibilityCloseAction

	// closeActionsMutex synchronizes access to closeActions.
	closeActionsMutex sync.Mutex
)

// EnsureCiVisibilityInitialization initializes CI visibility support if it hasn't been initialized already.
func EnsureCiVisibilityInitialization() {
	internalCiVisibilityInitialization()
}

func internalCiVisibilityInitialization() {
	ciVisibilityInitializationOnce.Do(func() {
		civisibility.SetState(civisibility.StateInitializing)
		defer civisibility.SetState(civisibility.StateInitialized)

		slog.SetLogLoggerLevel(slog.LevelInfo)
		// check the debug flag to enable debug logs. The tracer initialization happens
		// after the CI Visibility initialization so we need to handle this flag ourselves
		if enabled, _ := stableconfig.Bool("DD_TRACE_DEBUG", false); enabled {
			slog.SetLogLoggerLevel(slog.LevelDebug)
		}

		slog.Debug("civisibility: initializing")

		// Since calling this method indicates we are in CI Visibility mode, set the environment variable.
		_ = os.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")

		// Avoid sampling rate warning (in CI Visibility mode we send all data)
		_ = os.Setenv("DD_TRACE_SAMPLE_RATE", "1")

		// Preload all CI and Git tags.
		ciTags := utils.GetCITags()

		if _, ok := ciTags[constants.GitRepositoryURL]; !ok {
			slog.Debug("civisibility: git repository URL tag was not detected")
		}

		// Initializing additional features asynchronously.
		go func() { ensureAdditionalFeaturesInitialization(autoDetectServiceName) }()

		// Handle SIGINT and SIGTERM signals to ensure close actions run before exiting.
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-signals
			ExitCiVisibility()
			os.Exit(1)
		}()
	})
}

// PushCiVisibilityCloseAction adds a close action to be executed when CI visibility exits.
func PushCiVisibilityCloseAction(action ciVisibilityCloseAction) {
	closeActionsMutex.Lock()
	defer closeActionsMutex.Unlock()
	closeActions = append([]ciVisibilityCloseAction{action}, closeActions...)
}

// ExitCiVisibility executes all registered close actions.
func ExitCiVisibility() {
	if civisibility.GetState() != civisibility.StateInitialized {
		slog.Debug("civisibility: already closed or not initialized")
		return
	}

	civisibility.SetState(civisibility.StateExiting)
	defer civisibility.SetState(civisibility.StateExited)
	slog.Debug("civisibility: exiting")
	closeActionsMutex.Lock()
	defer closeActionsMutex.Unlock()
	defer func() {
		closeActions = []ciVisibilityCloseAction{}
		slog.Debug("civisibility: done.")
	}()
	for _, v := range closeActions {
		v()
	}
}
