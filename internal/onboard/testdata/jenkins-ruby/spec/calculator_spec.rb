require_relative "../lib/calculator"

RSpec.describe Calculator do
  it("adds") { expect(Calculator.new.add(1, 2)).to eq(3) }
  it("subtracts") { expect(Calculator.new.sub(3, 2)).to eq(1) }
end
