// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package stableconfig provides utilities to load and manage APM configurations
// loaded from YAML configuration files
package stableconfig

import (
	"errors"
	"fmt"
	"iter"
	"os"
	"strconv"
)

// Bool returns a boolean config value from managed file-based config, environment variable,
// or local file-based config, in that order. If none provide a valid boolean, it returns the default.
// Also returns the value's origin and any parse error encountered.
func Bool(env string, def bool) (value bool, err error) {
	for v := range stableConfigByPriority(env) {
		if val, err := strconv.ParseBool(v); err == nil {
			return val, nil
		}
		err = errors.Join(err, fmt.Errorf("non-boolean value for %s: '%s' in configuration, dropping", env, v))
	}
	return def, err
}

// String returns a string config value from managed file-based config, environment variable,
// or local file-based config, in that order. If none are set, it returns the default value and origin.
func String(env string, def string) string {
	for value := range stableConfigByPriority(env) {
		return value
	}
	return def
}

func stableConfigByPriority(env string) iter.Seq[string] {
	return func(yield func(string) bool) {
		if v, ok := os.LookupEnv(env); ok && !yield(v) {
			return
		}
	}
}
