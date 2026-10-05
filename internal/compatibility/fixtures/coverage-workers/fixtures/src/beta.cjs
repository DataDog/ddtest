function beta() {
  return 22;
}
module.exports = beta;
if (require.main === module) process.stdout.write(String(beta()));
