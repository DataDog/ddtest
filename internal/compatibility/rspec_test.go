package compatibility

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
)

func TestRSpecAdapterIntegration(t *testing.T) {
	rspecVersion := requireEnv(t, "DDTEST_RSPEC_VERSION")
	datadogVersion := requireEnv(t, "DDTEST_DATADOG_CI_VERSION")

	root := t.TempDir()
	writeFixture(t, root, "Gemfile", fmt.Sprintf(`source "https://rubygems.org"
gem "datadog-ci", %q
gem "rspec", %q
`, datadogVersion, rspecVersion))
	writeFixture(t, root, ".rspec", "--require spec_helper\n")
	writeFixture(t, root, "spec/spec_helper.rb", "DDTEST_RSPEC_SETUP = true\n")
	writeFixture(t, root, "spec/selected_spec.rb", `RSpec.describe "selected" do
  it "preserves configuration while running an assigned file" do
    expect(DDTEST_RSPEC_SETUP).to eq(true)
  end
end
`)
	writeFixture(t, root, "spec/unselected_spec.rb", `RSpec.describe "unselected" do
  it "must not run" do
    raise "unselected file ran"
  end
end
`)
	t.Chdir(root)

	rspec := framework.NewRSpec()
	rspec.SetPlatformEnv(map[string]string{"RUBYOPT": "-rbundler/setup -rdatadog/ci/auto_instrument"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	testFiles := discovery.TestFileSet{Pattern: rspec.TestPattern()}
	files, err := rspec.DiscoverTestFiles(ctx, testFiles)
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{"spec/selected_spec.rb", "spec/unselected_spec.rb"}
	requireFiles(t, files, wantFiles)

	tests, err := rspec.DiscoverTests(ctx, testFiles)
	if err != nil {
		t.Fatalf("full discovery failed: %v", err)
	}
	requireTestSources(t, tests, wantFiles)

	if err := rspec.RunTests(ctx, []string{"spec/selected_spec.rb"}, nil); err != nil {
		t.Fatalf("selected-file run failed: %v", err)
	}
}
