require "json"
require "etc"
require "rbconfig"

# Match the tracer's CRuby tag expressions without loading datadog-ci.
# https://github.com/DataDog/dd-trace-rb/blob/0b10368e0a2940c1f4d50c9739b9973b4f8bc32a/lib/datadog/core/environment/platform.rb
# Datadog::Core::Environment::Ext defines LANG_ENGINE and ENGINE_VERSION.
# TestRubyPlatformIntegration compares these values against the installed library.
engine_version = defined?(RUBY_ENGINE_VERSION) ? RUBY_ENGINE_VERSION : RUBY_VERSION
kernel_release = Etc.uname[:release] if RUBY_VERSION >= "2.2"

tags_map = {
  "os.platform" => RbConfig::CONFIG["host_os"],
  "os.architecture" => RbConfig::CONFIG["host_cpu"],
  "os.version" => kernel_release,
  "runtime.name" => RUBY_ENGINE,
  "runtime.version" => engine_version
}

output_file = ARGV[0]
File.write(output_file, tags_map.to_json)
