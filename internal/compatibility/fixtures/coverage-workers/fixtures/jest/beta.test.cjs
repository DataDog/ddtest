const { gate } = require('../gate.cjs');
const beta = require('../src/beta.cjs');
test('beta worker', async () => {
  expect(beta()).toBe(22);
  await gate();
});
