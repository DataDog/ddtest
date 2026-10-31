parallel(
  unit: {
    node('a') {
      sh 'bundle exec rake spec'
    }
  },
  lint: {
    node('b') {
      sh 'bundle exec rubocop'
    }
  }
)
