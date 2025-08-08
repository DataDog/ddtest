require "json"
require "fileutils"
require "datadog/core/environment/platform"

tags_map = {
  Datadog::CI::Ext::Test::TAG_OS_PLATFORM => ::RbConfig::CONFIG["host_os"],
  Datadog::CI::Ext::Test::TAG_OS_VERSION => Datadog::Core::Environment::Platform.kernel_release,
  Datadog::CI::Ext::Test::TAG_RUNTIME_NAME => Datadog::Core::Environment::Ext::LANG_ENGINE,
  Datadog::CI::Ext::Test::TAG_RUNTIME_VERSION => Datadog::Core::Environment::Ext::ENGINE_VERSION
}

FileUtils.mkdir_p(".dd")
File.write(".dd/runtime_tags.json", tags_map.to_json)
