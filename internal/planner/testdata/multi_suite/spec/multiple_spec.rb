if ENV["DDTEST_QA_FAIL_DISCOVERY"] == "true" && ENV["DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED"] == "1"
  raise "Requested full-discovery failure"
end

RSpec.describe "A" do
  it("runs A") { puts "RAN_A" }
end

RSpec.describe "B" do
  it("runs B") { puts "RAN_B" }
end
