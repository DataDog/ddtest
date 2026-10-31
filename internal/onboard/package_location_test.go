package onboard

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/settings"
	"github.com/stretchr/testify/require"
)

// subdirectoryRepository writes a Git checkout with a Mocha package in test/
// and the given repository files, as CI checks it out.
func subdirectoryRepository(t *testing.T, files map[string]string) string {
	t.Helper()
	root := CanonicalDirectory(t.TempDir())
	writeTree(t, root, map[string]string{
		"test/package.json":   `{"scripts":{"test":"mocha"},"devDependencies":{"mocha":"11"}}`,
		"test/schema.test.js": "require('../schema.json')\n",
		"schema.json":         "{}\n",
	})
	writeTree(t, root, files)
	output, err := exec.Command("git", "init", "-q", root).CombinedOutput()
	require.NoError(t, err, string(output))
	return root
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	}
}

func resetLocationSettings(t *testing.T) {
	t.Helper()
	cfg := settings.Get()
	old := *cfg
	cfg.Platform, cfg.Framework, cfg.Command = "", "", ""
	UseCIProvider("")
	t.Cleanup(func() {
		*cfg = old
		UseCIProvider("")
	})
}

const buildkiteCompose = "steps:\n  - label: test\n    command: npm test\n    plugins:\n      - docker-compose#v4.16.0:\n          config: test/docker-compose.yml\n          run: tests\n"

func TestLocatePackageAnchorsASubdirectoryAtTheCIRoot(t *testing.T) {
	resetLocationSettings(t)
	root := subdirectoryRepository(t, map[string]string{".buildkite/pipeline.yml": buildkiteCompose})
	t.Chdir(filepath.Join(root, "test"))
	location, ok := LocatePackage(filepath.Join(root, "test"))
	require.True(t, ok)
	require.Equal(t, PackageLocation{Root: root, Package: "test"}, location)

	nested := filepath.Join(root, "test", "valid-pipelines")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	_, ok = LocatePackage(nested)
	require.False(t, ok, "a directory without a package.json is not a package")
}

func TestLocatePackageKeepsAPackageWithItsOwnCIOrWithoutRootCI(t *testing.T) {
	resetLocationSettings(t)
	own := subdirectoryRepository(t, map[string]string{".buildkite/pipeline.yml": buildkiteCompose, "test/Jenkinsfile": "pipeline { agent any\n stages { stage('t') { steps { sh 'npm test' } } } }\n"})
	t.Chdir(filepath.Join(own, "test"))
	_, ok := LocatePackage(filepath.Join(own, "test"))
	require.False(t, ok, "a package with its own pipeline is onboarded where it is")

	bare := subdirectoryRepository(t, nil)
	t.Chdir(filepath.Join(bare, "test"))
	_, ok = LocatePackage(filepath.Join(bare, "test"))
	require.False(t, ok, "without CI at the root the package is the project")

	settings.Get().Platform = "ruby"
	ruby := subdirectoryRepository(t, map[string]string{".buildkite/pipeline.yml": buildkiteCompose})
	t.Chdir(filepath.Join(ruby, "test"))
	_, ok = LocatePackage(filepath.Join(ruby, "test"))
	require.False(t, ok, "another explicit platform keeps the current directory")
}

func TestLocatePackageSelectsTheRootPackageCIReferences(t *testing.T) {
	resetLocationSettings(t)
	root := subdirectoryRepository(t, map[string]string{".buildkite/pipeline.yml": buildkiteCompose, "docs/package.json": `{"scripts":{"build":"vite"}}`, "test/node_modules/mocha/package.json": `{}`})
	t.Chdir(root)
	location, ok := LocatePackage(root)
	require.True(t, ok)
	require.Equal(t, PackageLocation{Root: root, Package: "test"}, location, "only test/ is referenced by CI; installed dependencies are not packages")

	writeTree(t, root, map[string]string{"package.json": `{}`})
	_, ok = LocatePackage(root)
	require.False(t, ok, "a root with its own manifest is the project")
}

func TestLocatePackageNamesCandidatesWhenCIReferencesNoSinglePackage(t *testing.T) {
	resetLocationSettings(t)
	root := subdirectoryRepository(t, map[string]string{".gitlab-ci.yml": "a:\n  script:\n    - cd test && npm test\nb:\n  script:\n    - npm --prefix web test\n", "web/package.json": `{}`, "docs/package.json": `{}`})
	t.Chdir(root)
	location, ok := LocatePackage(root)
	require.True(t, ok)
	require.Empty(t, location.Package)
	require.Equal(t, "the repository root has no project manifest; JavaScript packages below it: test (referenced by .gitlab-ci.yml), web (referenced by .gitlab-ci.yml), docs. Run ddtest onboard in the package directory CI tests (for example cd test && ddtest onboard), or pass --platform javascript --framework <framework> here", location.Hint)

	empty := CanonicalDirectory(t.TempDir())
	_, ok = LocatePackage(empty)
	require.False(t, ok, "without candidates the generic detection error applies")
}

func TestPackageReferenceRecognizesPathsAndWorkingDirectories(t *testing.T) {
	for _, text := range []string{
		"config: test/docker-compose.yml",
		"run: cd test && npm test",
		"run: cd ./test; npm test",
		"- npm ci --prefix test",
		"- npm --prefix=test test",
		"working-directory: test",
		"        working-directory: ./test/",
		"working_directory: ~/project/test",
		"working_dir: /app/test",
		"dir('test') {",
		`dir("test") {`,
		"yarn --cwd test test",
	} {
		require.True(t, packageReference("test").MatchString(text), text)
	}
	for _, text := range []string{"npm test", "run: npm run test", "cd tests", "config: contest/docker-compose.yml", "working-directory: test-utils", "name: test"} {
		require.False(t, packageReference("test").MatchString(text), text)
	}
	require.True(t, packageReference("packages/app").MatchString("cd packages/app && npm test"))
}

// From the package directory, onboarding reads the CI configuration at the
// repository root and maps each provider's working directory to the package.
func TestCIDiscoveryFromASubdirectoryMapsStepsToThePackage(t *testing.T) {
	githubJob := func(steps string) string {
		return "on: push\njobs:\n  t:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - uses: actions/setup-node@v4\n        with:\n          node-version: 22\n" + steps
	}
	for name, files := range map[string]map[string]string{
		"github working-directory":      {".github/workflows/ci.yml": githubJob("      - run: npm ci\n        working-directory: test\n      - run: npm test\n        working-directory: test\n")},
		"github defaults":               {".github/workflows/ci.yml": "defaults:\n  run:\n    working-directory: test\n" + githubJob("      - run: npm ci\n      - run: npm test\n")},
		"github cd":                     {".github/workflows/ci.yml": githubJob("      - run: cd test && npm ci && npm test\n")},
		"github npm prefix":             {".github/workflows/ci.yml": githubJob("      - run: npm ci --prefix test\n      - run: npm --prefix test test\n")},
		"gitlab cd":                     {".gitlab-ci.yml": "test:\n  image: node:22\n  script:\n    - cd test\n    - npm ci\n    - npm test\n"},
		"circleci cd":                   {".circleci/config.yml": "version: 2.1\njobs:\n  test:\n    docker:\n      - image: cimg/node:22.0\n    steps:\n      - checkout\n      - run: cd test && npm ci\n      - run: cd test && npm test\nworkflows:\n  main:\n    jobs: [test]\n"},
		"buildkite compose WORKDIR":     {".buildkite/pipeline.yml": buildkiteCompose, "test/docker-compose.yml": "services:\n  tests:\n    build: .\n", "test/Dockerfile": "FROM node:22\nWORKDIR /app/test\nCOPY . /app/test\nRUN npm ci\n"},
		"buildkite compose working_dir": {".buildkite/pipeline.yml": "steps:\n  - label: test\n    command: npm test\n    plugins:\n      - docker-compose#v4.16.0:\n          run: tests\n", "docker-compose.yml": "services:\n  tests:\n    build: .\n    working_dir: /app/test\n    volumes:\n      - .:/app\n", "Dockerfile": "FROM node:22\n"},
		"buildkite cd":                  {".buildkite/pipeline.yml": "steps:\n  - label: test\n    commands:\n      - cd test\n      - npm ci\n      - npm test\n    plugins:\n      - docker#v5.9.0:\n          image: node:22\n"},
		"jenkins dir":                   {"Jenkinsfile": "pipeline {\n  agent any\n  stages {\n    stage('test') {\n      steps {\n        dir('test') {\n          sh 'npm ci'\n          sh 'npm test'\n        }\n      }\n    }\n  }\n}\n"},
	} {
		t.Run(name, func(t *testing.T) {
			resetLocationSettings(t)
			root := subdirectoryRepository(t, files)
			t.Chdir(filepath.Join(root, "test"))
			location, ok := LocatePackage(filepath.Join(root, "test"))
			require.True(t, ok)
			require.Equal(t, PackageLocation{Root: root, Package: "test"}, location)
			t.Chdir(location.Root)
			scope, err := DiscoverValidationScope(location.Root, "mocha")
			require.NoError(t, err)
			require.Empty(t, scope.Unresolved)
			require.Len(t, scope.Commands, 1)
			require.Equal(t, "test", scope.Commands[0].Directory)
		})
	}
}
