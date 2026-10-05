const { childCoverage } = require('../gate.cjs');
it('worker collects coverage in a subprocess', async function () {
  this.timeout(120000);
  await childCoverage();
});
