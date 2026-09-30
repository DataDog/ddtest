package testdrive

import (
	"os"
	"strings"
)

func rubyEnvironment(path string) map[string]string {
	env := map[string]string{}
	env["RUBYOPT"] = strings.TrimSpace(os.Getenv("RUBYOPT") + " -rbundler/setup -rdatadog/ci/auto_instrument")
	return env
}
