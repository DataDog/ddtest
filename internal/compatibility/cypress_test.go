package compatibility

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
)

func TestCypressAdapterIntegration(t *testing.T) {
	binary := requireEnv(t, "DDTEST_CYPRESS_BINARY")
	nodeModules := requireEnv(t, "DDTEST_CYPRESS_NODE_MODULES")
	resetSettingsAfterTest(t)

	root := t.TempDir()
	t.Chdir(root)
	tests := []struct {
		name           string
		projectName    string
		configFilename string
		configExport   string
		specPattern    any
		files          []string
		symlinkName    string
		symlinkTarget  string
		want           []string
	}{
		{
			name:           "minimatch extglob",
			projectName:    "extglob",
			configFilename: "cypress.config.ts",
			configExport:   "export default",
			specPattern:    "custom/**/*.@(spec|test).cy.ts",
			files: []string{
				"custom/discovered.spec.cy.ts",
				"custom/discovered.test.cy.ts",
				"custom/not-discovered.cy.ts",
			},
			want: []string{
				"extglob/custom/discovered.spec.cy.ts",
				"extglob/custom/discovered.test.cy.ts",
			},
		},
		{
			name:           "broad pattern excludes discovery wrapper",
			projectName:    "broad",
			configFilename: "cypress.config.js",
			configExport:   "module.exports =",
			specPattern:    "**/*.ts",
			files:          []string{"specs/discovered.ts", "specs/not-discovered.js"},
			want:           []string{"broad/specs/discovered.ts"},
		},
		{
			name:           "negated spec pattern subtracts matches",
			projectName:    "negated",
			configFilename: "cypress.config.js",
			configExport:   "module.exports =",
			specPattern:    []string{"**/*.cy.ts", "!**/slow.cy.ts"},
			files:          []string{"specs/fast.cy.ts", "specs/slow.cy.ts", "specs/helper.ts"},
			want:           []string{"negated/specs/fast.cy.ts"},
		},
		{
			name:           "symlinked spec directory",
			projectName:    "symlinked",
			configFilename: "cypress.config.js",
			configExport:   "module.exports =",
			specPattern:    "linked/**/*.cy.ts",
			files:          []string{"target/discovered.cy.ts"},
			symlinkName:    "linked",
			symlinkTarget:  "target",
			want:           []string{"symlinked/linked/discovered.cy.ts"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projectRoot := filepath.Join(root, test.projectName)
			if err := os.MkdirAll(projectRoot, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(nodeModules, filepath.Join(projectRoot, "node_modules")); err != nil {
				t.Fatal(err)
			}
			specPattern, err := json.Marshal(test.specPattern)
			if err != nil {
				t.Fatal(err)
			}
			config := test.configExport + ` {
  e2e: {
    supportFile: false,
    async setupNodeEvents(_on, config) {
      return { ...config, specPattern: ` + string(specPattern) + ` }
    },
  },
}
`
			writeFixture(t, projectRoot, test.configFilename, config)
			for _, filename := range test.files {
				writeFixture(t, projectRoot, filename, "")
			}
			if test.symlinkName != "" {
				if err := os.Symlink(test.symlinkTarget, filepath.Join(projectRoot, test.symlinkName)); err != nil {
					t.Fatal(err)
				}
			}

			configureFramework(shellCommand(binary, "run", "--project", test.projectName), "")
			cypress := framework.NewCypress()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			files, err := cypress.DiscoverTestFiles(ctx, discovery.TestFileSet{})
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			requireFiles(t, files, test.want)
		})
	}
}

func TestCypressAdapterExecutionIntegration(t *testing.T) {
	binary := requireEnv(t, "DDTEST_CYPRESS_BINARY")
	nodeModules := requireEnv(t, "DDTEST_CYPRESS_NODE_MODULES")
	resetSettingsAfterTest(t)

	root := t.TempDir()
	if err := os.Symlink(nodeModules, filepath.Join(root, "node_modules")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "cypress.config.js", `module.exports = {
  video: false,
  viewportWidth: 777,
  e2e: {
    supportFile: false,
    specPattern: 'cypress/e2e/**/*.cy.js',
  },
}
`)
	writeFixture(t, root, "cypress/e2e/selected.cy.js", `describe('selected', () => {
  it('runs an assigned file', () => {
    expect(true).to.equal(true)
    expect(Cypress.config('viewportWidth')).to.equal(777)
  })
})
`)
	writeFixture(t, root, "cypress/e2e/unselected.cy.js", `describe('unselected', () => {
  it('must not run', () => {
    throw new Error('unselected file ran')
  })
})
`)
	t.Chdir(root)

	command := []string{binary, "run"}
	if xvfb := os.Getenv("DDTEST_CYPRESS_XVFB"); xvfb != "" {
		command = []string{xvfb, "-a", binary, "run"}
	}
	configureFramework(shellCommand(command...), "")
	cypress := framework.NewCypress()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	files, err := cypress.DiscoverTestFiles(ctx, discovery.TestFileSet{Pattern: cypress.TestPattern()})
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{"cypress/e2e/selected.cy.js", "cypress/e2e/unselected.cy.js"}
	requireFiles(t, files, wantFiles)

	if err := cypress.RunTests(ctx, []string{"cypress/e2e/selected.cy.js"}, nil); err != nil {
		t.Fatalf("selected-file run failed: %v", err)
	}
}
