require "minitest/autorun"
require "calculator"

class CalculatorTest < Minitest::Test
  def test_add
    assert_equal 3, Calculator.new.add(1, 2)
  end
end
