// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package utils

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
)

func TestCodeOwnersPackageService(t *testing.T) {
	for _, tc := range []struct {
		name, enabled, service, format, rules, directory, want string
	}{
		{name: "default disabled", rules: "* @org/team", directory: "pkg"},
		{name: "disabled", enabled: "false", rules: "* @org/team", directory: "pkg"},
		{name: "explicit service", enabled: "true", service: "explicit", rules: "* @org/team", directory: "pkg"},
		{name: "default format", enabled: "true", rules: "* @org/team", directory: "pkg", want: "service-team"},
		{name: "dd-go format", enabled: "true", format: "dd-go-$(owner)", rules: "* @ddoghq/ci-app", directory: "pkg", want: "dd-go-ci-app"},
		{name: "upstream organization", enabled: "true", format: "dd-go-$(owner)", rules: "* @DataDog/ci-app", directory: "pkg", want: "dd-go-ci-app"},
		{name: "username", enabled: "true", rules: "* @user", directory: "pkg", want: "service-user"},
		{name: "email", enabled: "true", rules: "* ci@example.com", directory: "pkg", want: "service-ci@example.com"},
		{name: "email with slash", enabled: "true", rules: "* ci/tests@example.com", directory: "pkg", want: "service-ci/tests@example.com"},
		{name: "first owner", enabled: "true", rules: "* @org/first @org/second", directory: "pkg", want: "service-first"},
		{name: "last directory rule", enabled: "true", rules: "* @org/fallback\n/pkg/ @org/specific", directory: "pkg", want: "service-specific"},
		{name: "exact directory rule", enabled: "true", rules: "* @org/fallback\n/pkg @org/specific", directory: "pkg", want: "service-specific"},
		{name: "literal parent inheritance", enabled: "true", rules: "/pkg @org/parent", directory: "pkg/nested", want: "service-parent"},
		{name: "interior wildcard", enabled: "true", rules: "/pkg/*/tests/ @org/nested", directory: "pkg/team/tests/deep", want: "service-nested"},
		{name: "double star", enabled: "true", rules: "/pkg/**/mobile* @org/mobile", directory: "pkg/a/b/mobile-tests/deep", want: "service-mobile"},
		{name: "double star zero segments", enabled: "true", rules: "/pkg/**/mobile* @org/mobile", directory: "pkg/mobile-tests", want: "service-mobile"},
		{name: "unanchored directory", enabled: "true", rules: "tests/ @org/tests", directory: "pkg/tests/nested", want: "service-tests"},
		{name: "root anchored directory", enabled: "true", rules: "tests/ @org/tests\n/tests/ @org/root", directory: "pkg/tests/nested", want: "service-tests"},
		{name: "direct wildcard excludes descendants", enabled: "true", rules: "* @org/fallback\n/pkg/* @org/children", directory: "pkg/child/deep", want: "service-fallback"},
		{name: "direct children wildcard", enabled: "true", rules: "/pkg/* @org/children", directory: "pkg/child", want: "service-children"},
		{name: "owner removal", enabled: "true", rules: "* @org/team\n/pkg/", directory: "pkg", want: "service-not-owned"},
		{name: "no matching rule", enabled: "true", rules: "/another/ @org/team", directory: "pkg", want: "service-not-owned"},
		{name: "root package", enabled: "true", rules: "/* @org/root", directory: ".", want: "service-root"},
		{name: "repeated placeholder", enabled: "true", format: "$(owner)-$(owner)", rules: "* @org/team", directory: "pkg", want: "team-team"},
		{name: "literal format", enabled: "true", format: "literal-service", rules: "* @org/team", directory: "pkg", want: "literal-service"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			resetCodeOwnersTestState(t, root)
			ResetServiceFromCodeOwnersForTesting()
			t.Cleanup(ResetServiceFromCodeOwnersForTesting)
			t.Setenv(ServiceFromCodeOwnersEnv, tc.enabled)
			if tc.enabled == "" {
				if err := os.Unsetenv(ServiceFromCodeOwnersEnv); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv(ServiceFromCodeOwnersFormatEnv, tc.format)
			t.Setenv("DD_SERVICE", tc.service)
			writeCodeOwnersFile(t, filepath.Join(root, "CODEOWNERS"), tc.rules+"\n")
			registerCodeOwnersTestPackage(t, filepath.Join(root, tc.directory, "virtual_test.go"))
			if got := ServiceFromCodeOwners(); got != tc.want {
				t.Fatalf("service=%q, want %q", got, tc.want)
			}
			if got := os.Getenv("DD_SERVICE"); got != tc.service {
				t.Fatalf("DD_SERVICE was changed to %q", got)
			}
			if tc.enabled == "false" || tc.enabled == "" || tc.service != "" {
				if codeownersLookupDone {
					t.Fatal("disabled/explicit service performed CODEOWNERS discovery")
				}
			}
		})
	}
}

func TestCodeOwnersServiceMissingInputs(t *testing.T) {
	for _, tc := range []struct {
		name, content, want                string
		missing, outside, workspaceMissing bool
	}{
		{name: "missing file", missing: true},
		{name: "invalid rule is diagnosed", content: strings.Repeat("x", 70*1024), want: "service-not-owned"},
		{name: "outside workspace", content: "* @org/team\n", outside: true},
		{name: "missing workspace", content: "* @org/team\n", workspaceMissing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			resetCodeOwnersTestState(t, root)
			ResetServiceFromCodeOwnersForTesting()
			t.Cleanup(ResetServiceFromCodeOwnersForTesting)
			t.Setenv(ServiceFromCodeOwnersEnv, "true")
			t.Setenv("DD_SERVICE", "")
			if !tc.missing {
				writeCodeOwnersFile(t, filepath.Join(root, "CODEOWNERS"), tc.content)
			}
			source := filepath.Join(root, "pkg", "virtual_test.go")
			if tc.outside {
				source = filepath.Join(t.TempDir(), "virtual_test.go")
			}
			if tc.workspaceMissing {
				originalCiTags = map[string]string{}
			}
			registerCodeOwnersTestPackage(t, source)
			if got := ServiceFromCodeOwners(); got != tc.want {
				t.Fatalf("fallback overridden: %q", got)
			}
		})
	}
}

func TestCodeOwnersServiceCacheConcurrentReaders(t *testing.T) {
	root := t.TempDir()
	resetCodeOwnersTestState(t, root)
	ResetServiceFromCodeOwnersForTesting()
	t.Cleanup(ResetServiceFromCodeOwnersForTesting)
	t.Setenv(ServiceFromCodeOwnersEnv, "true")
	t.Setenv(ServiceFromCodeOwnersFormatEnv, "dd-go-$(owner)")
	t.Setenv("DD_SERVICE", "")
	writeCodeOwnersFile(t, filepath.Join(root, "CODEOWNERS"), "/pkg/ @org/team\n")
	registerCodeOwnersTestPackage(t, filepath.Join(root, "pkg", "virtual_test.go"))
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			if got := ServiceFromCodeOwners(); got != "dd-go-team" {
				t.Errorf("service=%q", got)
			}
		})
	}
	workers.Wait()
	writeCodeOwnersFile(t, filepath.Join(root, "CODEOWNERS"), "/pkg/ @org/changed\n")
	if got := ServiceFromCodeOwners(); got != "dd-go-team" {
		t.Fatalf("process identity changed: %q", got)
	}
	t.Setenv("DD_SERVICE", "explicit-later")
	if got := ServiceFromCodeOwners(); got != "" {
		t.Fatalf("cache overrode explicit service: %q", got)
	}
}

func TestCodeOwnersPackageDirectoryPaths(t *testing.T) {
	root := t.TempDir()
	packageDir := filepath.Join(root, "nested", "pkg")
	if err := os.MkdirAll(packageDir, 0755); err != nil {
		t.Fatal(err)
	}
	tags := map[string]string{constants.CIWorkspacePath: root, constants.GitRepositoryURL: "https://example.com/repo.git"}
	for _, location := range []testPackageLocation{
		{source: filepath.Join(packageDir, "virtual_test.go")},
		{source: "example.com/repo/nested/pkg/virtual_test.go", workingDirectory: root},
		{source: "example.com/repo/nested/pkg/virtual_test.go", workingDirectory: filepath.Join(root, "different")},
		{source: "alias/module/pkg/virtual_test.go", workingDirectory: packageDir},
	} {
		got, ok := codeOwnersPackageDirectory(root, tags, &location)
		if !ok || got != "nested/pkg" {
			t.Fatalf("%+v: %q %t", location, got, ok)
		}
	}
}

// Overlay files are virtual, but their source package directories exist. Keep
// that invariant when TMPDIR itself is a symlink, as on macOS runners.
func registerCodeOwnersTestPackage(t *testing.T, source string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(source), 0755); err != nil {
		t.Fatal(err)
	}
	RegisterTestPackageSource(source)
}

func TestCodeOwnersPackageDirectorySymlink(t *testing.T) {
	root := t.TempDir()
	packageDir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(packageDir, 0755); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.Symlink(root, workspace); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	for _, source := range []string{
		filepath.Join(packageDir, "virtual_test.go"),
		filepath.Join(workspace, "pkg", "virtual_test.go"),
	} {
		directory, ok := codeOwnersPackageDirectory(workspace, nil, &testPackageLocation{source: source})
		if !ok || directory != "pkg" {
			t.Fatalf("source=%q: directory=%q inside=%t", source, directory, ok)
		}
	}
}
