const beta = require('../instrumented/beta.cjs');
it('beta worker', () => { expect(beta()).to.equal(22); cy.task('gate'); });
