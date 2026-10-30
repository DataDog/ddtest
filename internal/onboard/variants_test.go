package onboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVariantPreparationPreservesMatrixAndConditions(t *testing.T) {
	root := newJestRepository(t, `jobs:
  built:
    strategy:
      matrix: {build: [cjs, esm]}
    steps:
      - run: pnpm run build
      - if: matrix.build == 'cjs'
        run: sed -i~ 's/src/dist/' jest.config.mts
      - if: matrix.build == 'esm'
        run: sed -i~ 's/src/dist\/esm/' jest.config.mts
      - run: pnpm test
        env: {NODE_ENV: development}
  react:
    strategy:
      matrix: {react: [18.0.0, 19.0.0]}
    steps:
      - run: |
          pnpm add -D react@$REACT_VER react-dom@$REACT_VER && pnpm test
        env: {REACT_VER: '${{ matrix.react }}'}
  source:
    steps:
      - run: pnpm test
`)
	writeScripts(t, root, map[string]string{"test": "jest run", "build": "rollup -c"})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	var variants []string
	for _, command := range scope.Commands {
		if len(command.Prerequisites) == 0 {
			variants = append(variants, "source")
			continue
		}
		if command.Environment["REACT_VER"] != "" {
			variants = append(variants, "react:"+command.Environment["REACT_VER"])
			continue
		}
		variants = append(variants, command.Prerequisites[1].Command)
	}
	require.ElementsMatch(t, []string{"source", "react:18.0.0", "react:19.0.0", `sed -i~ s/src/dist/ jest.config.mts`, `sed -i~ s/src/dist\\/esm/ jest.config.mts`}, variants)
	for _, command := range scope.Commands {
		require.Equal(t, "jest run", command.Command)
		switch {
		case command.Environment["REACT_VER"] != "":
			version := command.Environment["REACT_VER"]
			require.Equal(t, []BuildCommand{{Directory: ".", Command: "pnpm add -D react@" + version + " react-dom@" + version, Kind: "dependencies", Environment: map[string]string{"REACT_VER": version}}}, command.Prerequisites)
		case command.Environment["NODE_ENV"] == "development":
			patch := `sed -i~ s/src/dist/ jest.config.mts`
			if command.locations[0].Matrix["build"] == "esm" {
				patch = `sed -i~ s/src/dist\\/esm/ jest.config.mts`
			}
			require.Equal(t, []BuildCommand{
				{Directory: ".", Command: "rollup -c"},
				{Directory: ".", Command: patch, Kind: "patch"},
			}, command.Prerequisites)
		default:
			require.Empty(t, command.Prerequisites)
			require.Empty(t, command.Environment)
		}
	}
}

func TestVariantExpansionDoesNotExecuteShellOrUseAmbientVariables(t *testing.T) {
	for _, command := range []string{"pnpm add -D react@$UNDECLARED && pnpm test", "pnpm add -D react@$(touch marker) && pnpm test"} {
		_, _, err := resolveVariantStep(ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command, Env: map[string]string{"REACT_VER": "18"}}, nil)
		require.Error(t, err)
	}
	step, env, err := resolveVariantStep(ciWorkflow{}, runtimeJob{}, runtimeStep{Run: `sed -i~ "1s/^/import.meta.env.MODE='${NODE_ENV}';/" tests/example.tsx`, Env: map[string]string{"NODE_ENV": "development"}}, nil)
	require.NoError(t, err)
	require.Equal(t, "development", env["NODE_ENV"])
	require.Contains(t, step.Run, "development")
	require.NotContains(t, step.Run, "${NODE_ENV}")
	for _, command := range []string{"sed -i 's/x/y/e' config.js", "sed -i 's/x/y/' ../config.js", "pnpm add -D pkg@https://example.com/pkg.tgz"} {
		words, err := staticCommands(command)
		require.NoError(t, err)
		_, ok := localPreparation("/repo", "/repo", words[0])
		require.False(t, ok, command)
	}
}

func TestDiscoveryPreservesExplicitEmptyNodeOptions(t *testing.T) {
	for _, value := range []string{"", "-r ${{ env.DD_TRACE_PACKAGE }}"} {
		root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - run: tsc\n        env: {NODE_OPTIONS: '"+value+"'}\n      - run: jest\n        env: {NODE_OPTIONS: '"+value+"'}\n")
		scope, err := DiscoverValidationScope(root, "jest")
		require.NoError(t, err)
		require.Len(t, scope.Commands, 1)
		require.Contains(t, scope.Commands[0].Environment, "NODE_OPTIONS")
		require.Empty(t, scope.Commands[0].Environment["NODE_OPTIONS"])
		require.Len(t, scope.Commands[0].Prerequisites, 1)
		require.Contains(t, scope.Commands[0].Prerequisites[0].Environment, "NODE_OPTIONS")
		require.Empty(t, scope.Commands[0].Prerequisites[0].Environment["NODE_OPTIONS"])
	}
}

func TestCredentialSecretsAreValidatedWithoutAndReported(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"jest"}}`), 0600))
	path := filepath.Join(root, ".github/workflows/ci.yml")
	discover := func(workflow string) ValidationScope {
		t.Helper()
		require.NoError(t, os.WriteFile(path, []byte(workflow), 0600))
		scope, err := DiscoverValidationScope(root, "jest")
		require.NoError(t, err)
		return scope
	}
	// A setup step's, the job's and the test step's own credentials.
	for _, workflow := range []string{
		"jobs:\n  test:\n    steps:\n      - run: npm ci\n        env: {NPM_TOKEN: '${{ secrets.NPM_TOKEN }}'}\n      - run: npm test\n",
		"jobs:\n  test:\n    env: {NPM_TOKEN: '${{ secrets.ACCESS_TOKEN }}'}\n    steps:\n      - run: npm ci\n      - run: npm test\n",
		"jobs:\n  test:\n    steps:\n      - run: npm ci\n      - run: npm test\n        env: {NPM_TOKEN: '${{ github.token }}'}\n",
	} {
		scope := discover(workflow)
		require.Len(t, scope.Commands, 1, workflow)
		require.Empty(t, scope.Unresolved, workflow)
		require.Empty(t, scope.Commands[0].UnvalidatedSetup, workflow)
		require.Equal(t, []string{"NPM_TOKEN"}, scope.Commands[0].WithoutSecrets, workflow)
		require.NotContains(t, scope.Commands[0].Environment, "NPM_TOKEN", "no secret reaches validation")
		require.Contains(t, strings.Join(scope.Caveats, "\n"), "step 2 (npm test): validated without the CI secrets NPM_TOKEN", workflow)
	}
	// A test step that sets the variable itself keeps its value, while the
	// setup step that read the secret still ran without it.
	scope := discover("jobs:\n  test:\n    steps:\n      - run: npm ci\n        env: {NPM_TOKEN: '${{ secrets.NPM_TOKEN }}'}\n      - run: npm test\n        env: {NPM_TOKEN: local-value}\n")
	require.Len(t, scope.Commands, 1)
	require.Equal(t, []string{"NPM_TOKEN"}, scope.Commands[0].WithoutSecrets)
	require.Equal(t, "local-value", scope.Commands[0].Environment["NPM_TOKEN"])
	// Identical configurations merge, keeping every location's omitted secrets.
	scope = discover("jobs:\n  plain:\n    steps:\n      - run: npm test\n  sonar:\n    steps:\n      - run: npm test\n        env: {SONAR_TOKEN: '${{ secrets.SONAR_TOKEN }}'}\n")
	require.Len(t, scope.Commands, 1)
	require.Equal(t, []string{"SONAR_TOKEN"}, scope.Commands[0].WithoutSecrets)
	// A secret after the last test can't reach it.
	scope = discover("jobs:\n  test:\n    steps:\n      - run: npm test\n      - run: npx codecov\n        env: {CODECOV_TOKEN: '${{ secrets.CODECOV_TOKEN }}'}\n")
	require.Len(t, scope.Commands, 1)
	require.Empty(t, scope.Commands[0].WithoutSecrets)
	// A command that reads the credential, a secret used as a setting and a
	// value that only contains a secret still require review.
	for _, workflow := range []string{
		"jobs:\n  test:\n    steps:\n      - run: npm test -- --token=$NPM_TOKEN\n        env: {NPM_TOKEN: '${{ secrets.NPM_TOKEN }}'}\n",
		"jobs:\n  test:\n    steps:\n      - run: npm test\n        env: {API_URL: '${{ secrets.API_URL }}'}\n",
		"jobs:\n  test:\n    steps:\n      - run: npm test\n        env: {AUTH_MODE: '${{ secrets.AUTH_MODE }}'}\n",
		"jobs:\n  test:\n    steps:\n      - run: npm test\n        env: {PASSWORD_POLICY: '${{ secrets.PASSWORD_POLICY }}'}\n",
		"jobs:\n  test:\n    steps:\n      - run: npm test\n        env: {AUTH: 'Bearer ${{ secrets.TOKEN }}'}\n",
	} {
		scope := discover(workflow)
		blocked := len(scope.Unresolved) > 0
		for _, entry := range scope.Commands {
			blocked = blocked || len(entry.UnvalidatedSetup) > 0
		}
		require.True(t, blocked, workflow)
	}
}

func TestCredentialNamesEndWithACredential(t *testing.T) {
	for _, name := range []string{"GITHUB_TOKEN", "SONAR_TOKEN", "NPM_AUTH", "AUTH", "SONAR_LOGIN_PASSWORD", "AWS_SECRET_ACCESS_KEY", "CLIENT_SECRET", "GPG_PASSPHRASE", "OPENAI_API_KEY", "DJANGO_SECRET_KEY", "GOOGLE_CREDENTIALS",
		// Deploy logins and signing keys, as graphql-java's publishing jobs set.
		"MAVEN_CENTRAL_USER", "SONATYPE_USERNAME", "SONAR_LOGIN", "MAVEN_CENTRAL_PGP_KEY", "GPG_KEY", "SIGNING_KEY", "DEPLOY_SSH_KEY",
		// Qualifiers that keep the value a credential.
		"MAVEN_CENTRAL_PASSWORD_NEW", "MAVEN_CENTRAL_USER_NEW", "NPM_TOKEN_OLD", "API_KEY_V2", "SONAR_TOKEN_2", "AWS_SECRET_ACCESS_KEY_PROD",
		"SIGNING_KEY_BASE64", "GPG_PRIVATE_KEY_B64", "DB_PASSWORD_STAGING_ENCODED", "TOKEN_CI"} {
		require.True(t, credentialName.MatchString(name), name)
	}
	for _, name := range []string{"AUTH_MODE", "PASSWORD_POLICY", "TOKEN_URL", "API_URL", "OAUTH", "SECRETS_DIR",
		// A credential followed by a setting word names a setting.
		"TOKEN_TYPE", "SECRET_PATH", "USER_ID", "USER_AGENT", "LOGIN_URL", "PGP_KEY_ID", "API_KEY_FILE", "PASSWORD_NEW_POLICY", "TOKEN_V2_URL",
		// Words that only end like a credential.
		"CACHE_KEY", "MONKEY", "SUPERUSER", "AUTH_V"} {
		require.False(t, credentialName.MatchString(name), name)
	}
}
