// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build unix && !darwin

package osinfo

// macOSProductVersion is only reached when runtime.GOOS is darwin.
func macOSProductVersion() (string, bool) { return "", false }
