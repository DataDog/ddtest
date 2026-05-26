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
