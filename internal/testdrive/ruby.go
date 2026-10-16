package testdrive

import (
	"context"
	"os"
	"strings"
)

func rubyEnvironmentInfo(ctx context.Context) environmentInfo {
	info := environmentInfo{Source: "Ruby from PATH", Runtime: "Ruby"}
	_ = probeEnvironment(ctx, "ruby", []string{"-rjson", "-rrbconfig", "-e", `File.write(ARGV.fetch(0), JSON.generate(executable: RbConfig.ruby, version: RUBY_VERSION))`}, &info)
	return info
}

func rubyEnvironment(path string) map[string]string {
	env := map[string]string{}
	env["RUBYOPT"] = strings.TrimSpace(os.Getenv("RUBYOPT") + " -rbundler/setup -rdatadog/ci/auto_instrument")
	return env
}
