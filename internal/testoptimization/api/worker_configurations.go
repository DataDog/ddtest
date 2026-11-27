package api

import (
	"strings"

	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/telemetry"
)

// NewTransportWithConfigurations binds requests to a worker without changing process tags.
func NewTransportWithConfigurations(serviceName string, level settings.TestSkippingLevel, tags map[string]string, telemetryClient telemetry.Client) Transport {
	base := newTransportWithServiceNameAndSubdomain(serviceName, "api", level, telemetryClient)
	if base == nil {
		return nil
	}
	client := base.(*transport)
	custom := map[string]string{}
	for key, value := range tags {
		if name, ok := strings.CutPrefix(key, "test.configuration."); ok {
			custom[name] = value
		}
	}
	client.testConfigurations = testConfigurations{
		OsPlatform: tags["os.platform"], OsVersion: tags["os.version"], OsArchitecture: tags["os.architecture"],
		RuntimeName: tags["runtime.name"], RuntimeVersion: tags["runtime.version"],
		RuntimeArchitecture: tags["runtime.architecture"], TestBundle: tags["test.bundle"], Custom: custom,
	}
	return client
}
