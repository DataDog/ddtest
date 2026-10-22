package testdrive

import (
	"fmt"
	"net"
	"net/url"
	"slices"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
)

// Bound the pre-launch check without waiting for application readiness.
const cypressPortCheckTimeout = 200 * time.Millisecond

func checkCypressStartupPorts(plan *onboard.CypressCommand, environments ...map[string]string) error {
	if plan == nil || len(plan.Start) == 0 {
		return nil
	}
	addresses := slices.Clone(plan.WaitOn)
	for _, env := range environments {
		if value := env["CYPRESS_BASE_URL"]; value != "" {
			addresses = append(addresses, value)
		}
	}
	for _, value := range addresses {
		address, err := url.Parse(value)
		if err != nil || address.Port() == "" {
			continue
		}
		ip := net.ParseIP(address.Hostname())
		if address.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			continue
		}
		connection, err := net.DialTimeout("tcp", address.Host, cypressPortCheckTimeout)
		if err == nil {
			_ = connection.Close()
			return fmt.Errorf("cypress startup port %s is already occupied. Preserve that listener. Use a documented application/test port override consistently in the startup command, Cypress baseUrl and action wait-on URL; retain their CI defaults", address.Host)
		}
	}
	return nil
}
