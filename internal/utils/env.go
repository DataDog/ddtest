// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package utils

import (
	"log/slog"
	"os"
	"strconv"
)

// BoolEnv returns the parsed boolean value of an environment variable, or def otherwise.
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

// IntEnv returns the parsed integer value of an environment variable, or def otherwise.
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
