# frozen_string_literal: true

# Rails isolation exits forked tests with exit!, bypassing async delivery.
# Use the same CI transport, but send each finished trace synchronously.
if defined?(Datadog::CI) && Datadog.configuration.ci.enabled && Datadog::Tracing.enabled?
  require "datadog/tracing/sync_writer"
  transport = Datadog.configuration.tracing.test_mode.writer_options.fetch(:transport)
  Datadog.configure do |config|
    config.tracing.writer = Datadog::Tracing::SyncWriter.new(transport: transport)
  end
end
