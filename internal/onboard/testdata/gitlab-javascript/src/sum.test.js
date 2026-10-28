const sum = require('./sum')

test('adds', () => {
  expect(sum(1, 2)).toBe(3)
})

test('adds negatives', () => {
  expect(sum(-1, -2)).toBe(-3)
})
