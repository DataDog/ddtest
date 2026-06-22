// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package osinfo

// Modified in init functions to provide OS-specific information.
var osVersion = "unknown"

// OSVersion returns the operating system release, e.g. major/minor version
// number and build ID.
func OSVersion() string {
	return osVersion
}
