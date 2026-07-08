# GitHub Actions Example

The plan job chooses the CI node count and emits a matrix; the run job downloads
the artifacts and executes only the files assigned to its CI node. Each matrix
job is one CI node, and `matrix.ci_node_index` is passed to
`ddtest run --ci-node`.

```yaml
name: CI with DDTest

on: [push]

env:
  DD_TEST_OPTIMIZATION_RUNNER_PLATFORM: ruby
  DD_TEST_OPTIMIZATION_RUNNER_FRAMEWORK: rspec
  DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM: 1
  DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM: 8

jobs:
  dd_plan:
    runs-on: ubuntu-latest
    outputs:
      matrix: ${{ steps.dd_plan.outputs.matrix }}
    steps:
      - uses: actions/checkout@v4
      - name: Download ddtest binary
        run: |
          mkdir -p bin
          gh release download --repo DataDog/ddtest --pattern "ddtest-linux-amd64" --dir bin
          mv bin/ddtest-linux-amd64 bin/ddtest
          chmod +x bin/ddtest
        env:
          GH_TOKEN: ${{ github.token }}
      - name: Setup Ruby
        uses: ruby/setup-ruby@v1
        with:
          bundler-cache: true
      - name: Configure Datadog Test Optimization
        uses: datadog/test-visibility-github-action@v2
        with:
          languages: ruby
          api_key: ${{ secrets.DD_API_KEY }}
          site: datadoghq.com
      - id: dd_plan
        name: Plan test execution with DDTest
        run: bin/ddtest plan
      - uses: actions/upload-artifact@v4
        with:
          name: dd-artifacts
          path: .testoptimization
          include-hidden-files: true

  dd_test:
    runs-on: ubuntu-latest
    needs: [dd_plan]
    strategy:
      fail-fast: false
      matrix: ${{ fromJson(needs.dd_plan.outputs.matrix) }}
    steps:
      - uses: actions/checkout@v4
      - name: Download ddtest binary
        run: |
          mkdir -p bin
          gh release download --repo DataDog/ddtest --pattern "ddtest-linux-amd64" --dir bin
          mv bin/ddtest-linux-amd64 bin/ddtest
          chmod +x bin/ddtest
        env:
          GH_TOKEN: ${{ github.token }}
      - uses: actions/download-artifact@v4
        with:
          name: dd-artifacts
          path: .testoptimization
      - name: Setup Ruby
        uses: ruby/setup-ruby@v1
        with:
          bundler-cache: true
      - name: Configure Datadog Test Optimization
        uses: datadog/test-visibility-github-action@v2
        with:
          languages: ruby
          api_key: ${{ secrets.DD_API_KEY }}
          site: datadoghq.com
      - name: Run tests
        run: bin/ddtest run --ci-node ${{ matrix.ci_node_index }}
```

DDTest automatically writes the matrix file at `.testoptimization/github/config`
that looks like:

```text
matrix={"include":[{"ci_node_index":0,"ci_node_total":3},{"ci_node_index":1,"ci_node_total":3},{"ci_node_index":2,"ci_node_total":3}]}
```

In GitHub Actions, `ddtest plan` also writes this matrix to `$GITHUB_OUTPUT`, so
the plan step can expose it directly as `steps.dd_plan.outputs.matrix`.

## Python / Pytest Variant

Use the same plan/test job structure for Python projects, but configure the
runner and setup steps for pytest:

```yaml
env:
  DD_TEST_OPTIMIZATION_RUNNER_PLATFORM: python
  DD_TEST_OPTIMIZATION_RUNNER_FRAMEWORK: pytest
  DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM: 1
  DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM: 8
```

Replace each Ruby setup step with Python dependency installation:

```yaml
- name: Setup Python
  uses: actions/setup-python@v5
  with:
    python-version: "3.12"
    cache: pip
- name: Install Python dependencies
  run: python -m pip install -r requirements.txt "ddtrace>=4.11.0" pytest
```

Configure Datadog Test Optimization for Python:

```yaml
- name: Configure Datadog Test Optimization
  uses: datadog/test-visibility-github-action@v2
  with:
    languages: python
    api_key: ${{ secrets.DD_API_KEY }}
    site: datadoghq.com
```

The `ddtest plan` and `ddtest run --ci-node ${{ matrix.ci_node_index }}`
commands can stay the same when the platform and framework are provided through
the environment.

## JavaScript / Jest Variant

Use the same plan/test job structure for JavaScript projects, but configure the
runner and setup steps for Jest:

```yaml
env:
  DD_TEST_OPTIMIZATION_RUNNER_PLATFORM: javascript
  DD_TEST_OPTIMIZATION_RUNNER_FRAMEWORK: jest
  DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM: 1
  DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM: 8
```

Replace each Ruby setup step with Node.js dependency installation:

```yaml
- name: Setup Node.js
  uses: actions/setup-node@v4
  with:
    node-version: "22"
    cache: npm
- name: Install JavaScript dependencies
  run: npm ci
```

Configure Datadog Test Optimization for JavaScript:

```yaml
- name: Configure Datadog Test Optimization
  uses: datadog/test-visibility-github-action@v2
  with:
    languages: js
    api_key: ${{ secrets.DD_API_KEY }}
    site: datadoghq.com
```

DDTest sets `NODE_OPTIONS=-r dd-trace/ci/init` for Jest worker processes, so the
project dependencies installed before `ddtest plan` must include `dd-trace`.
The `ddtest plan` and `ddtest run --ci-node ${{ matrix.ci_node_index }}`
commands can stay the same when the platform and framework are provided through
the environment.
