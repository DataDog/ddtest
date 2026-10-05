const { gate } = require('../gate.cjs');
const alpha = require('../src/alpha.cjs');
test('alpha worker', async () => {
  expect(alpha()).toBe(11);
  await gate();
});
