# Best Practices

## Optimize Planning Step

When using ddtest, you need to add a planning step that performs test discovery
(e.g., RSpec dry-run) before execution. This stage adds overhead: you can
optimize it with the practices below.

### Preinstall System Dependencies Via Docker

Bake OS packages (e.g., ImageMagick) into a base image so they're cached in
layers and not installed on every run.

```dockerfile
# ci/Dockerfile.test
FROM ruby:3.3
RUN apt-get update && DEBIAN_FRONTEND=noninteractive \
    apt-get install -y --no-install-recommends imagemagick libpq-dev \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /app
```

### Cache Project Dependencies

Use your CI's dependency cache. For GitHub Actions + Bundler:

```yaml
- uses: ruby/setup-ruby@v1
  with:
    ruby-version: 3.3
    bundler-cache: true
```

### Disable Seeds/Fixtures During Discovery

Discovery (planning) does not execute tests; you don't have to setup DB,
migrations, or seeds. You could guard DB-related code when running in discovery
mode determined by `DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED=1`.

RSpec / Rails example:

```ruby
# in seeds.rb
return if ENV["DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED"].present?
# your seeds here

# in rails_helper.rb
ActiveRecord::Migration.maintain_test_schema! unless ENV["DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED"].present?

RSpec.configure do |config|
  unless ENV["DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED"]
    config.use_transactional_fixtures = true
  else
    config.use_transactional_fixtures = false
    config.use_active_record = false
  end
end
```

After these changes the tests discovery will be faster and will not fail when
database is not present. You can skip database setup for planning step completely
and save a lot of time.

### Cache Test Discovery

If full discovery still spends most of its time loading the application, DB
fixtures, or framework helpers, cache DDTest's discovery file between CI runs.
The restored cache path can be passed with either the CLI flag or environment
variable:

```bash
ddtest plan --test-discovery-cache .ddtest-cache/tests-discovery.json

DD_TEST_OPTIMIZATION_RUNNER_TEST_DISCOVERY_CACHE=.ddtest-cache/tests-discovery.json ddtest plan
```

DDTest imports the restored file before planning, validates it, and skips full
discovery when the cache is still safe to use. After planning, save the refreshed
internal cache file for the next run:

```bash
if [ -f .testoptimization/tests-discovery/tests.json ]; then
  mkdir -p .ddtest-cache
  cp .testoptimization/tests-discovery/tests.json .ddtest-cache/tests-discovery.json
fi
```

For GitHub Actions, the flow can look like this:

```yaml
- uses: actions/cache@v4
  with:
    path: .ddtest-cache/tests-discovery.json
    key: ddtest-discovery-${{ github.ref_name }}
    restore-keys: |
      ddtest-discovery-

- name: Plan tests
  env:
    DD_TEST_OPTIMIZATION_RUNNER_TEST_DISCOVERY_CACHE: .ddtest-cache/tests-discovery.json
  run: ddtest plan

- name: Save latest discovery cache
  if: always()
  run: |
    if [ -f .testoptimization/tests-discovery/tests.json ]; then
      mkdir -p .ddtest-cache
      cp .testoptimization/tests-discovery/tests.json .ddtest-cache/tests-discovery.json
    fi
```

DDTest ignores the cache and runs full discovery when the file is missing,
corrupt, produced for a different platform/framework/test location/exclude
pattern, or based on a commit that is not available locally. It also invalidates
the cache when files under the current project's test root changed. For example,
the default RSpec root is `spec/**` and the default Minitest root is `test/**`;
with `--tests-location custom/spec/**/*_spec.rb`, the root is `custom/**`.

In monorepos, run DDTest from the project subdirectory whose tests you are
planning. Cache invalidation is scoped to that project's effective test root, so
changes in sibling projects do not invalidate its discovery cache.

## Minitest Support In Non-Rails Projects

We use `bundle exec rake test` command when we don't detect `rails` command to
run tests. This command doesn't have a built in way to pass the list of files to
execute, so we pass them as a space-separated list of files in `TEST_FILES`
environment variable.

You need to use this environment variable in your project to integrate your tests
with `ddtest run`. Example when using `Rake::TestTask`:

```ruby
Rake::TestTask.new(:test) do |test|
  test.test_files = ENV["TEST_FILES"] ? ENV["TEST_FILES"].split : ["test/**/*.rb"]
end
```
