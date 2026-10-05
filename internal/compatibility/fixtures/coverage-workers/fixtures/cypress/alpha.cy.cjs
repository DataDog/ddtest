const alpha = require('../instrumented/alpha.cjs');
it('alpha worker', () => { expect(alpha()).to.equal(11); cy.task('gate'); });
