# Repository and CI command discovery

This component reads GitHub Actions workflows and local `package.json` scripts
to identify Jest commands. It follows literal script aliases, working directories,
matrix values, environment settings and repository-local composite actions.
Repeated commands retain all of their CI locations. Recognized build prerequisites
are recorded as metadata; unknown setup and unsupported shell expressions remain
visible rather than being treated as validated.

Discovery does not install packages, run repository commands, edit workflows or
validate the tracer. Existing `ddtest onboard` and `ddtest testdrive` behavior is
unchanged in this step. The discovery component currently accepts Jest only.

## Manual QA harness

Run these commands in the ddtest repository root, using the Go version in
`go.mod`. No Node installation, repository dependencies or Datadog credentials
are needed. Keep this shell open through all three scenarios. The harness lives
under the repository root so Go permits its import of the internal package.
The trap removes only the two directories created here.

```sh
task_harness_dir=$(mktemp -d "$PWD/.ddtest-discovery-qa.XXXXXX")
task_fixture_dir=$(mktemp -d)
trap 'rm -rf "$task_harness_dir" "$task_fixture_dir"' EXIT

cat > "$task_harness_dir/main.go" <<'GO'
package main

import (
    "encoding/json"
    "log"
    "os"

    "github.com/DataDog/ddtest/internal/onboard"
)

func main() {
    if len(os.Args) != 2 {
        log.Fatal("usage: discovery-harness REPOSITORY")
    }
    scope, err := onboard.DiscoverValidationScope(os.Args[1], "jest")
    if err != nil {
        log.Fatal(err)
    }
    encoder := json.NewEncoder(os.Stdout)
    encoder.SetIndent("", "  ")
    if err := encoder.Encode(scope); err != nil {
        log.Fatal(err)
    }
}
GO

mkdir -p "$task_fixture_dir/.github/workflows" "$task_fixture_dir/app"
cat > "$task_fixture_dir/app/package.json" <<'JSON'
{"scripts":{"coverage":"npm run test -- --coverage","test":"jest --config jest.config.js"}}
JSON
cat > "$task_fixture_dir/.github/workflows/ci.yml" <<'YAML'
name: Tests
on: push
jobs:
  test:
    strategy:
      matrix:
        node: [20, 22]
    defaults:
      run:
        working-directory: app
    env:
      TZ: UTC
    steps:
      - run: npm run coverage
YAML
go run "$task_harness_dir/main.go" "$task_fixture_dir"
```

Expect one command, `jest --config jest.config.js --coverage`, with directory
`app`, environment `TZ=UTC` and two locations (one per matrix entry). There
should be no unresolved commands. This confirms that alias expansion preserves
arguments and that equivalent matrix consumers are grouped.

Replace the scripts with a cycle and repeat discovery:

```sh
cat > "$task_fixture_dir/app/package.json" <<'JSON'
{"scripts":{"coverage":"npm run test","test":"npm run coverage"}}
JSON
go run "$task_harness_dir/main.go" "$task_fixture_dir"
```

Expect no resolved commands and an `unresolved` diagnostic mentioning the cycle.
The harness must return promptly rather than repeatedly expanding the aliases.

Finally, use dynamic setup followed by an identifiable test command:

```sh
cat > "$task_fixture_dir/app/package.json" <<'JSON'
{"scripts":{"coverage":"node setup.js > setup.log && jest --config jest.config.js"}}
JSON
go run "$task_harness_dir/main.go" "$task_fixture_dir"
test ! -e "$task_fixture_dir/app/setup.log"
```

Expect the Jest command to remain visible with unresolved setup recorded in both
`unresolved` and the command's `unvalidated_setup`. The final shell check must
succeed: discovery must not execute `node setup.js` or create `setup.log`.

Exit the shell when finished to remove the harness and fixture, or run the trap's
cleanup command explicitly and then clear the trap.
