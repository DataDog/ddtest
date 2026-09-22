package framework

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFrameworksDetectTheirProjects(t *testing.T) {
	tests := []struct {
		name      string
		framework Framework
		files     map[string]string
		dirs      []string
	}{
		{name: "rspec", framework: NewRSpec(), files: map[string]string{"Gemfile": `gem "rspec"`}},
		{name: "minitest", framework: NewMinitest(), dirs: []string{"test"}},
		{name: "pytest", framework: NewPytest(), files: map[string]string{"pytest.ini": "[pytest]"}},
		{name: "jest", framework: NewJest(), files: map[string]string{"package.json": `{"scripts":{"test":"jest"},"devDependencies":{"jest":"latest"}}`}},
		{name: "mocha", framework: NewMocha(), files: map[string]string{"package.json": `{"devDependencies":{"mocha":"latest"}}`}},
		{name: "cypress", framework: NewCypress(), files: map[string]string{"package.json": `{"devDependencies":{"cypress":"latest"}}`}},
		{name: "playwright", framework: NewPlaywright(), files: map[string]string{"package.json": `{"devDependencies":{"@playwright/test":"latest"}}`}},
		{name: "cucumber", framework: NewCucumber(), files: map[string]string{"package.json": `{"devDependencies":{"@cucumber/cucumber":"latest"}}`}},
		{name: "vitest", framework: NewVitest(), files: map[string]string{"package.json": `{"devDependencies":{"vitest":"latest"}}`}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repositoryRoot := t.TempDir()
			detected, err := test.framework.Detect(repositoryRoot)
			if err != nil {
				t.Fatalf("Detect() unexpected error: %v", err)
			}
			if detected {
				t.Fatal("Detect() = true for an empty repository")
			}

			for _, dir := range test.dirs {
				if err := os.MkdirAll(filepath.Join(repositoryRoot, dir), 0755); err != nil {
					t.Fatal(err)
				}
			}
			for filename, contents := range test.files {
				if err := os.WriteFile(filepath.Join(repositoryRoot, filename), []byte(contents), 0644); err != nil {
					t.Fatal(err)
				}
			}

			detected, err = test.framework.Detect(repositoryRoot)
			if err != nil {
				t.Fatalf("Detect() unexpected error: %v", err)
			}
			if !detected {
				t.Fatal("Detect() = false for a matching repository")
			}
		})
	}
}

func TestJestDetectUsesProjectTestScript(t *testing.T) {
	repositoryRoot := t.TempDir()
	manifest := `{"scripts":{"test":"jest"},"devDependencies":{"jest":"latest"}}`
	if err := os.WriteFile(filepath.Join(repositoryRoot, "package.json"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}

	jest := NewJest()
	detected, err := jest.Detect(repositoryRoot)
	if err != nil {
		t.Fatalf("Detect() unexpected error: %v", err)
	}
	if !detected {
		t.Fatal("Detect() = false")
	}
	command, args := jest.TestCommand(nil)
	if command != "npm" {
		t.Fatalf("TestCommand() command = %q, want npm", command)
	}
	wantArgs := []string{"test", "--", "--runInBand"}
	if len(args) != len(wantArgs) {
		t.Fatalf("TestCommand() args = %v, want %v", args, wantArgs)
	}
	for i := range wantArgs {
		if args[i] != wantArgs[i] {
			t.Fatalf("TestCommand() args = %v, want %v", args, wantArgs)
		}
	}
}
