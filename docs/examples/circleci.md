# CircleCI Example

This example uses a setup workflow: the first config runs `ddtest plan`, stores
the generated `.testoptimization/` directory, and continues into a second config
with the selected CI node count. Each CircleCI parallel container is one CI node,
and `CIRCLE_NODE_INDEX` is passed to `ddtest run --ci-node`.

In `.circleci/config.yml`:

```yaml
version: '2.1'
setup: true

orbs:
  node: circleci/node@7
  ruby: circleci/ruby@2
  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@1
  continuation: circleci/continuation@0.2.0

jobs:
  plan:
    docker:
      - image: cimg/ruby:3.4.1-node
    environment:
      RAILS_ENV: test
      DD_ENV: ci
      BUNDLE_PATH: vendor/bundle
      BUNDLE_JOBS: 4
    steps:
      - checkout
      - ruby/install-deps
      - node/install-packages:
          pkg-manager: yarn
      - test-optimization-circleci-orb/autoinstrument:
          languages: ruby
          site: datadoghq.eu
      - run:
          name: Download ddtest latest release
          command: |
            set -euo pipefail
            mkdir -p bin
            curl -fsSL https://github.com/DataDog/ddtest/releases/latest/download/ddtest-linux-amd64 -o bin/ddtest
            chmod +x bin/ddtest
      - run:
          name: Plan tests with ddtest
          command: ./bin/ddtest plan --platform ruby --framework minitest
          environment:
            DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM: 1
            DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM: 4
      - save_cache:
          key: ddtest-plan-{{ .Revision }}
          paths:
            - .testoptimization
            - bin/ddtest
      - run:
          name: Determine CI node count
          command: |
            set -euo pipefail
            cat .testoptimization/runner/parallel-runners.txt
            desired=$(cat .testoptimization/runner/parallel-runners.txt 2>/dev/null || echo 1)
            if ! echo "${desired}" | grep -Eq '^[0-9]+$'; then
              echo "Invalid parallelism value '${desired}', defaulting to 1"
              desired=1
            fi
            if [ "${desired}" -lt 1 ]; then
              echo "Parallelism must be at least 1, defaulting to 1"
              desired=1
            fi
            printf '{"parallelism": %s}\n' "${desired}" > pipeline-parameters.json
            cat pipeline-parameters.json
      - continuation/continue:
          configuration_path: .circleci/test.yml
          parameters: pipeline-parameters.json

workflows:
  plan:
    jobs:
      - plan
```

In `.circleci/test.yml`:

```yaml
version: '2.1'

parameters:
  parallelism:
    type: integer
    default: 1

orbs:
  node: circleci/node@7
  ruby: circleci/ruby@2
  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@1

jobs:
  test:
    parallelism: << pipeline.parameters.parallelism >>
    docker:
      - image: cimg/ruby:3.4.1-browsers
    environment:
      RAILS_ENV: test
      DD_ENV: ci
      BUNDLE_PATH: vendor/bundle
      BUNDLE_JOBS: 4
    steps:
      - checkout
      - restore_cache:
          keys:
            - ddtest-plan-{{ .Revision }}
      - ruby/install-deps
      - node/install-packages:
          pkg-manager: yarn
      - test-optimization-circleci-orb/autoinstrument:
          languages: ruby
          site: datadoghq.eu
      - run:
          name: Precompile assets
          command: |
            bundle exec rails assets:precompile
      - run:
          name: Run tests with ddtest
          command: |
            NODE_INDEX=${CIRCLE_NODE_INDEX:-0}
            export DD_TEST_SESSION_NAME="quotes-rails-ci-${NODE_INDEX}"
            ./bin/ddtest run --platform ruby --framework minitest --ci-node "${NODE_INDEX}"

workflows:
  test:
    jobs:
      - test
```
