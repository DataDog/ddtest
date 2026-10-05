module.exports = {
  rootDir: '..',
  testMatch: ['**/jest/*.test.cjs'],
  collectCoverage: true,
  coverageProvider: 'babel',
  coverageReporters: ['json'],
  coverageDirectory: process.env.REPRO_REPORT,
  testTimeout: 120000,
};
