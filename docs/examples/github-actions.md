# GitHub Actions Example

The plan job computes the split and emits a matrix; the run job downloads the
artifacts and executes only its slice.

```yaml
name: CI with DDTest

on: [push]

env:
  DD_TEST_OPTIMIZATION_RUNNER_PLATFORM: ruby
  DD_TEST_OPTIMIZATION_RUNNER_FRAMEWORK: rspec

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
        env:
          DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM: 1
          DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM: 8
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
        env:
          DD_TEST_SESSION_NAME: ddtest-runner-${{ matrix.ci_node_index }}
```

DDTest automatically writes the matrix file at `.testoptimization/github/config`
that looks like:

```text
matrix={"include":[{"ci_node_index":0,"ci_node_total":3},{"ci_node_index":1,"ci_node_total":3},{"ci_node_index":2,"ci_node_total":3}]}
```

In GitHub Actions, `ddtest plan` also writes this matrix to `$GITHUB_OUTPUT`, so
the plan step can expose it directly as `steps.dd_plan.outputs.matrix`.
