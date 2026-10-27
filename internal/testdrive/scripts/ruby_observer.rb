# frozen_string_literal: true

# A companion observer retains the customer's reporters and command. Results are
# independent of Datadog's events, including when tracing is disabled or broken.
require "json"
require "fileutils"

module DDTestObserver
  TESTS = []
  ERRORS = []

  def self.write
    directory = ENV.fetch("DDTEST_RUBY_RESULTS")
    FileUtils.mkdir_p(directory)
    File.write(File.join(directory, "#{Process.pid}.json"), JSON.generate(complete: true, tests: TESTS, errors: ERRORS))
  end

  def self.failure(exception)
    exception ? "#{exception.class}: #{exception.message}" : ""
  end

  def self.telemetry_name(name)
    # Apply the installed release's pure wire-name formatting (for example,
    # arrays become ARRAY). Outcomes and executions still come from the native
    # reporter, independently of tracing or any received telemetry.
    defined?(Datadog::CI::Utils::TestName) ? Datadog::CI::Utils::TestName.normalize(name) : name
  end
end

if ENV.fetch("DDTEST_RUBY_FRAMEWORK") == "rspec"
  require "rspec/core"

  class DDTestRSpecFormatter
    RSpec::Core::Formatters.register self, :example_passed, :example_failed, :example_pending, :message, :close

    def initialize(output); end

    def example_passed(notification)
      record(notification.example, "passed")
    end

    def example_failed(notification)
      record(notification.example, "failed")
    end

    def example_pending(notification)
      record(notification.example, "pending")
    end

    def message(notification)
      DDTestObserver::ERRORS << notification.message if notification.message.include?("An error occurred")
    end

    def record(example, status)
      metadata = example.metadata
      group = metadata[:example_group]
      group = group[:parent_example_group] while group[:parent_example_group]
      # Derive the expected wire name from native metadata.
      name = example.full_description.strip.sub(group[:description].to_s, "").strip
      file = File.expand_path(metadata[:file_path])
      # Scoped IDs distinguish examples even when named descriptions repeat.
      # They come from native metadata, independently of the tracer's events.
      DDTestObserver::TESTS << {file: file, name: example.full_description, telemetry_name: DDTestObserver.telemetry_name(name),
                               telemetry_scoped_id: metadata[:scoped_id],
                               status: status, failure: DDTestObserver.failure(example.exception)}
    end

    def close(notification)
      DDTestObserver.write
    end
  end

  module DDTestRSpecConfiguration
    def configure(*args)
      super
      @configuration.add_formatter("progress") if @configuration.formatters.empty?
      @configuration.add_formatter(DDTestRSpecFormatter)
      if (probe = ENV["DDTEST_RUBY_PROBE"]) && !probe.empty?
        # Rake's --pattern adds matching files even with an explicit file list.
        @configuration.force(pattern: probe)
        @configuration.files_or_directories_to_run = [probe]
      end
    end
  end
  RSpec::Core::Runner.prepend(DDTestRSpecConfiguration)
else
  require "minitest"

  class DDTestMinitestReporter < Minitest::AbstractReporter
    def record(result)
      klass = Minitest::Runnable.runnables.find { |candidate| candidate.name == result.class_name }
      location = klass&.instance_method(result.name)&.source_location
      DDTestObserver::TESTS << {file: location ? File.expand_path(location.first) : "", class_name: result.class_name, name: result.name,
                               telemetry_name: DDTestObserver.telemetry_name(result.name),
                               status: result.skipped? ? "pending" : (result.passed? ? "passed" : "failed"),
                               failure: result.failures.map { |failure| DDTestObserver.failure(failure) }.join("\n")}
    end

    def report
      DDTestObserver.write
    end
  end

  # Register through Minitest's plugin lifecycle so existing reporters and Rails
  # setup remain in place, including when rake starts a child Ruby interpreter.
  module Minitest
    def self.plugin_ddtest_init(options)
      reporter << DDTestMinitestReporter.new
    end
  end
  Minitest.extensions << "ddtest"

  module DDTestMinitestRun
    def run(*args)
      if (probe = ENV["DDTEST_RUBY_PROBE"]) && !probe.empty?
        # A Rake task can launch several Minitest processes. Elect one owner so
        # the pass control and an aborting fail control exercise the same probe.
        begin
          File.open(ENV.fetch("DDTEST_PROBE_OWNER"), File::WRONLY | File::CREAT | File::EXCL) { |file| file.puts Process.pid }
          @ddtest_probe_owner = true
        rescue Errno::EEXIST
          # Further runs in the owning process keep their normal retry lifecycle.
        end
        if @ddtest_probe_owner
          require probe
          Minitest::Runnable.runnables.select! { |klass| klass.name == "DDTestValidationProbe" }
        else
          Minitest::Runnable.runnables.clear
        end
      end
      super
    end
  end
  Minitest.singleton_class.prepend(DDTestMinitestRun)
end
