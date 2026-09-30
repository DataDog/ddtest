package nodeoptions

import "testing"

func TestCIRequireOptions(t *testing.T) {
	for _, test := range []struct {
		name     string
		options  string
		without  string
		absolute string
		has      bool
	}{
		{name: "package name", options: "-r dd-trace/ci/init --max-old-space-size=256", without: "--max-old-space-size=256", has: true},
		{name: "absolute path", options: "--require=/tmp/node_modules/dd-trace/ci/init.js --trace-warnings", without: "--trace-warnings", absolute: "/tmp/node_modules/dd-trace/ci/init.js", has: true},
		{name: "quoted path and loader", options: `--require "/tmp/project loader.cjs" -r "/tmp/action install/dd-trace/ci/init.js" --max-old-space-size=256`, without: `--require "/tmp/project loader.cjs" --max-old-space-size=256`, absolute: "/tmp/action install/dd-trace/ci/init.js", has: true},
		{name: "attached short option", options: "-r/tmp/dd-trace/ci/init.js --trace-warnings", without: "--trace-warnings", absolute: "/tmp/dd-trace/ci/init.js", has: true},
		{name: "unrelated preload", options: "--require /tmp/unrelated/init.js --trace-warnings", without: "--require /tmp/unrelated/init.js --trace-warnings"},
		{name: "similar package", options: "-r dd-trace/ci/initializer", without: "-r dd-trace/ci/initializer"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := HasRequire(test.options, "dd-trace/ci/init"); got != test.has {
				t.Fatalf("HasRequire() = %t, want %t", got, test.has)
			}
			if got := AbsoluteRequire(test.options, "dd-trace/ci/init"); got != test.absolute {
				t.Fatalf("AbsoluteRequire() = %q, want %q", got, test.absolute)
			}
			if got := WithoutRequire(test.options, "dd-trace/ci/init"); got != test.without {
				t.Fatalf("WithoutRequire() = %q, want %q", got, test.without)
			}
		})
	}
}

func TestRegisterImportOptions(t *testing.T) {
	options := `--import="/tmp/action install/dd-trace/register.js" --require "/tmp/loader.cjs"`
	if !HasImport(options, "dd-trace/register.js") {
		t.Fatal("absolute register import was not found")
	}
	if got := WithoutImport(options, "dd-trace/register.js"); got != `--require "/tmp/loader.cjs"` {
		t.Fatalf("WithoutImport() = %q", got)
	}
}
