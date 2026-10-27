For Minitest suites using `ActiveSupport::Testing::Isolation` (forked tests that call `exit!`), async trace delivery can lose completed tests. Keep isolation, test bodies, helpers, reporters and parallelism unchanged. Configure synchronous trace delivery in **both CI and local validation**, rather than patching tests or weakening telemetry matching.

Create `ci/datadog_test_optimization.rb` (merge with existing CI configuration if present):

```ruby
__FORK_SETUP__
```

On the existing test step, after the Datadog action, append the initializer to the action's exported options:

```yaml
env:
  RUBYOPT: "${{ env.RUBYOPT }} -r./ci/datadog_test_optimization.rb"
```

Adjust the relative path only if the test step has another working directory. Preserve existing Ruby options and their order. The initializer keeps the configured Datadog transport and is inactive in the uninstrumented baseline. Synchronous delivery trades batching for reliable delivery before the child exits; full testdrive must still verify every native identity, coverage and all supported features. Do not add `Datadog.shutdown!` to customer test methods, remove isolation or skip tests. If evidence remains incomplete, report it instead of modifying the suite to obtain a pass.
