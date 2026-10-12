// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package globalconfig stores immutable CI process and session identifiers.
package globalconfig

import (
	"crypto/rand"
	"encoding/hex"
	"os"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/env"
)

const rootSessionIDEnvVar = "_DD_ROOT_GO_SESSION_ID"

var cfg = newConfig()

type config struct {
	runtimeID, rootSessionID string
	// startedSession is set when this process started its root session, and
	// set rootSessionIDEnvVar for its child processes; callerValue and
	// callerSet are the variable as the process received it.
	startedSession bool
	callerValue    string
	callerSet      bool
}

func newConfig() *config {
	id := newRuntimeID()
	value, set := os.LookupEnv(rootSessionIDEnvVar)
	started := env.Get(rootSessionIDEnvVar) == ""
	return &config{runtimeID: id, rootSessionID: getRootSessionID(id), startedSession: started, callerValue: value, callerSet: set}
}
func RuntimeID() string     { return cfg.runtimeID }
func RootSessionID() string { return cfg.rootSessionID }

// RootSessionEnv returns the environment entry that makes a child process
// join this process's root session.
func RootSessionEnv() string { return rootSessionIDEnvVar + "=" + cfg.rootSessionID }

// KeepCallerEnvironment restores the variable that this package set to hand a
// root session that this process started to its child processes, as the
// process received it. ddto calls it: it passes its caller's environment to
// go test unchanged and hands its session only to its own worker, with
// RootSessionEnv.
func KeepCallerEnvironment() {
	switch {
	case !cfg.startedSession:
	case cfg.callerSet:
		_ = os.Setenv(rootSessionIDEnvVar, cfg.callerValue)
	default:
		_ = os.Unsetenv(rootSessionIDEnvVar)
	}
}

// newRuntimeID creates a UUIDv4 with the same wire format as the SDK runtime ID.
func newRuntimeID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	var encoded [36]byte
	hex.Encode(encoded[0:8], id[0:4])
	hex.Encode(encoded[9:13], id[4:6])
	hex.Encode(encoded[14:18], id[6:8])
	hex.Encode(encoded[19:23], id[8:10])
	hex.Encode(encoded[24:36], id[10:16])
	encoded[8], encoded[13], encoded[18], encoded[23] = '-', '-', '-', '-'
	return string(encoded[:])
}

func getRootSessionID(runtimeID string) string {
	id := env.Get(rootSessionIDEnvVar)
	if id == "" {
		id = runtimeID
	}
	os.Setenv(rootSessionIDEnvVar, id) // propagate to child processes
	return id
}

// InstrumentationInstallID returns the install ID as described in DD_INSTRUMENTATION_INSTALL_ID
func InstrumentationInstallID() string {
	return env.Get("DD_INSTRUMENTATION_INSTALL_ID")
}

// InstrumentationInstallType returns the install type as described in DD_INSTRUMENTATION_INSTALL_TYPE
func InstrumentationInstallType() string {
	return env.Get("DD_INSTRUMENTATION_INSTALL_TYPE")
}

// InstrumentationInstallTime returns the install time as described in DD_INSTRUMENTATION_INSTALL_TIME
func InstrumentationInstallTime() string {
	return env.Get("DD_INSTRUMENTATION_INSTALL_TIME")
}
