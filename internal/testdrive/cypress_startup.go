package testdrive

import (
	"fmt"
	"github.com/DataDog/ddtest/internal/onboard"
	"net"
	"net/url"
	"time"
)

func checkCypressStartupPorts(plan *onboard.CypressCommand) error {
	if plan == nil || len(plan.Start) == 0 {
		return nil
	}
	for _, value := range plan.WaitOn {
		address, err := url.Parse(value)
		if err != nil || address.Port() == "" {
			continue
		}
		ip := net.ParseIP(address.Hostname())
		if address.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			continue
		}
		connection, err := net.DialTimeout("tcp", address.Host, 200*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return fmt.Errorf("cypress startup port %s is already occupied. Preserve that listener. Use a documented application/test port override consistently in the startup command, Cypress baseUrl and action wait-on URL; retain their CI defaults", address.Host)
		}
	}
	return nil
}
