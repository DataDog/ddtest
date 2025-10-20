require "json"

require "datadog/ci/ext/test"
require "datadog/core/environment/platform"

tags_map = {
  Datadog::CI::Ext::Test::TAG_OS_PLATFORM => ::RbConfig::CONFIG["host_os"],
  Datadog::CI::Ext::Test::TAG_OS_ARCHITECTURE => ::RbConfig::CONFIG["host_cpu"],
  Datadog::CI::Ext::Test::TAG_OS_VERSION => Datadog::Core::Environment::Platform.kernel_release,
  Datadog::CI::Ext::Test::TAG_RUNTIME_NAME => Datadog::Core::Environment::Ext::LANG_ENGINE,
  Datadog::CI::Ext::Test::TAG_RUNTIME_VERSION => Datadog::Core::Environment::Ext::ENGINE_VERSION
}

output_file = ARGV[0]
File.write(output_file, tags_map.to_json)
