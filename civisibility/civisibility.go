// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package civisibility

import (
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
)

type State int

const (
	StateUninitialized State = iota
	StateInitializing
	StateInitialized
	StateExiting
	StateExited
)

const (
	DefaultAgentHostname  = "localhost"
	DefaultTraceAgentPort = "8126"
)

// This is a variable rather than a constant so it can be replaced in unit tests
var DefaultTraceAgentUDSPath = "/var/run/datadog/apm.socket"

var (
	status     atomic.Int32
	isTestMode atomic.Bool
)

func GetState() State {
	// Get the state atomically
	return State(status.Load())
}

func SetState(state State) {
	// Set the state atomically
	status.Store(int32(state))
}

func SetTestMode() {
	isTestMode.Store(true)
}

func IsTestMode() bool {
	return isTestMode.Load()
}

// AgentURLFromEnv resolves the URL for the trace agent based on
// the default host/port and UDS path, and via standard environment variables.
// AgentURLFromEnv has the following priority order:
//   - First, DD_TRACE_AGENT_URL if it is set
//   - Then, if either of DD_AGENT_HOST and DD_TRACE_AGENT_PORT are set,
//     use http://DD_AGENT_HOST:DD_TRACE_AGENT_PORT,
//     defaulting to localhost and 8126, respectively
//   - Then, DefaultTraceAgentUDSPath, if the path exists
//   - Finally, localhost:8126
func AgentURLFromEnv() *url.URL {
	if agentURL := os.Getenv("DD_TRACE_AGENT_URL"); agentURL != "" {
		u, err := url.Parse(agentURL)
		if err != nil {
			slog.Warn("Failed to parse DD_TRACE_AGENT_URL", "error", err.Error())
		} else {
			switch u.Scheme {
			case "unix", "http", "https":
				return u
			default:
				slog.Warn("Unsupported protocol in Agent URL. Must be one of: http, https, unix.", "scheme", u.Scheme, "url", agentURL)
			}
		}
	}

	host, providedHost := os.LookupEnv("DD_AGENT_HOST")
	port, providedPort := os.LookupEnv("DD_TRACE_AGENT_PORT")
	if host == "" {
		// We treat set but empty the same as unset
		providedHost = false
		host = DefaultAgentHostname
	}
	if port == "" {
		// We treat set but empty the same as unset
		providedPort = false
		port = DefaultTraceAgentPort
	}
	httpURL := &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(host, port),
	}
	if providedHost || providedPort {
		return httpURL
	}

	if _, err := os.Stat(DefaultTraceAgentUDSPath); err == nil {
		return &url.URL{
			Scheme: "unix",
			Path:   DefaultTraceAgentUDSPath,
		}
	}
	return httpURL
}

// BoolEnv returns the parsed boolean value of an environment variable, or
// def otherwise.
func BoolEnv(key string, def bool) bool {
	vv, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	v, err := strconv.ParseBool(vv)
	if err != nil {
		slog.Warn("Non-boolean value for env var, defaulting to default value", "key", key, "default", def, "error", err.Error())
		return def
	}
	return v
}

// IntEnv returns the parsed int value of an environment variable, or
// def otherwise.
func IntEnv(key string, def int) int {
	vv, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	v, err := strconv.Atoi(vv)
	if err != nil {
		slog.Warn("Non-integer value for env var, defaulting to default value", "key", key, "default", def, "error", err.Error())
		return def
	}
	return v
}

// ForEachStringTag runs fn on every key val pair encountered in str.
// str may contain multiple key val pairs separated by either space
// or comma (but not a mixture of both), and each key val pair is separated by a delimiter.
func ForEachStringTag(str string, delimiter string, fn func(key string, val string)) {
	sep := " "
	if strings.Contains(str, ",") {
		// falling back to comma as separator
		sep = ","
	}
	for _, tag := range strings.Split(str, sep) {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		kv := strings.SplitN(tag, delimiter, 2)
		key := strings.TrimSpace(kv[0])
		if key == "" {
			continue
		}
		var val string
		if len(kv) == 2 {
			val = strings.TrimSpace(kv[1])
		}
		fn(key, val)
	}
}

// DDTagsDelimiter is the separator between key-val pairs for DD env vars
const DDTagsDelimiter = ":"

// ParseTagString returns tags parsed from string as map
func ParseTagString(str string) map[string]string {
	res := make(map[string]string)
	ForEachStringTag(str, DDTagsDelimiter, func(key, val string) { res[key] = val })
	return res
}
