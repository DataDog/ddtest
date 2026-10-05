module.exports = {
  'temp-dir': process.env.REPRO_TEMP,
  'report-dir': process.env.REPRO_REPORT,
  reporter: ['json'],
  include: ['src/*.cjs'],
  extension: ['.cjs'],
  exclude: [],
  cache: false,
};
