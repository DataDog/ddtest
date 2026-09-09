package compatibility

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
)

func TestMinitestAdapterIntegration(t *testing.T) {
	minitestVersion := requireEnv(t, "DDTEST_MINITEST_VERSION")
	datadogVersion := requireEnv(t, "DDTEST_DATADOG_CI_VERSION")

	root := t.TempDir()
	writeFixture(t, root, "Gemfile", fmt.Sprintf(`source "https://rubygems.org"
gem "datadog-ci", %q
gem "minitest", %q
gem "rake", "13.2.1"
`, datadogVersion, minitestVersion))
	writeFixture(t, root, "Rakefile", `require "rake/testtask"

Rake::TestTask.new(:test) do |test|
  test.test_files = ENV["TEST_FILES"] ? ENV["TEST_FILES"].split : FileList["test/**/*_test.rb"]
end

task default: :test
`)
	writeFixture(t, root, "test/test_helper.rb", `gem "minitest", ENV.fetch("DDTEST_MINITEST_VERSION")
require "minitest/autorun"
DDTEST_MINITEST_SETUP = true
`)
	writeFixture(t, root, "test/selected_test.rb", `require_relative "test_helper"

class SelectedTest < Minitest::Test
  def test_preserves_setup_while_running_an_assigned_file
    assert DDTEST_MINITEST_SETUP
  end
end
`)
	writeFixture(t, root, "test/unselected_test.rb", `require_relative "test_helper"

class UnselectedTest < Minitest::Test
  def test_must_not_run
    flunk "unselected file ran"
  end
end
`)
	t.Chdir(root)

	minitest := framework.NewMinitest()
	minitest.SetPlatformEnv(map[string]string{"RUBYOPT": "-rbundler/setup -rdatadog/ci/auto_instrument"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	testFiles := discovery.TestFileSet{Pattern: minitest.TestPattern()}
	files, err := minitest.DiscoverTestFiles(ctx, testFiles)
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{"test/selected_test.rb", "test/unselected_test.rb"}
	requireFiles(t, files, wantFiles)

	tests, err := minitest.DiscoverTests(ctx, testFiles)
	if err != nil {
		t.Fatalf("full discovery failed: %v", err)
	}
	requireTestSources(t, tests, wantFiles)

	if err := minitest.RunTests(ctx, []string{"test/selected_test.rb"}, nil); err != nil {
		t.Fatalf("selected-file run failed: %v", err)
	}
}
