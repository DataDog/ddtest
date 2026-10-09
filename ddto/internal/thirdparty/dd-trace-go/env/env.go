// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package env

import (
	"os"
	"slices"
)

// keyAliases maps canonical configuration keys to their known aliases. Only
// the aliases of keys read by the CI runtime are retained.
var keyAliases = map[string][]string{
	"DD_API_KEY": {"DD-API-KEY"},
}

// sensitiveConfigurations is the set of configuration keys whose value must not
// be reported in configuration telemetry.
var sensitiveConfigurations = map[string]struct{}{
	"DD_API_KEY": {},
	"DD_APP_KEY": {},
}

// Get returns the value of the environment variable. If it is empty, the value
// of the first non-empty alias is returned.
func Get(name string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}

	for _, alias := range keyAliases[name] {
		if v := os.Getenv(alias); v != "" {
			return v
		}
	}

	return ""
}

// Lookup is a wrapper around os.LookupEnv. If the environment variable is not
// set, the first alias that is set is returned.
func Lookup(name string) (string, bool) {
	if v, ok := os.LookupEnv(name); ok {
		return v, true
	}

	for _, alias := range keyAliases[name] {
		if v, ok := os.LookupEnv(alias); ok {
			return v, true
		}
	}

	return "", false
}

// IsSensitive reports whether the given configuration name must not have its value
// reported in configuration telemetry. A name is sensitive if it is listed in
// sensitiveConfigurations, or if it is an alias of such a key.
func IsSensitive(name string) bool {
	if _, ok := sensitiveConfigurations[name]; ok {
		return true
	}
	for key, aliases := range keyAliases {
		if _, ok := sensitiveConfigurations[key]; !ok {
			continue
		}
		if slices.Contains(aliases, name) {
			return true
		}
	}
	return false
}
