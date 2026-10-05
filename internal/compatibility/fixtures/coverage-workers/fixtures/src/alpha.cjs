function alpha() {
  return 11;
}
module.exports = alpha;
if (require.main === module) process.stdout.write(String(alpha()));
