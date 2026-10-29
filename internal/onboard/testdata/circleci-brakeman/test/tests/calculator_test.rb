require 'minitest/autorun'
require_relative '../../lib/calculator'

class CalculatorTest < Minitest::Test
  def test_add
    assert_equal 3, Calculator.add(1, 2)
  end
end
