def lib = library('ci-steps@v2')

node {
  sh 'bundle exec rake spec'
}
