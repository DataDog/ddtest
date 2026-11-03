parallel(
  unit: {
    datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['RUBY']]) {
      node('a') {
        sh 'bundle exec rake spec'
      }
    }
  },
  lint: {
    node('b') {
      sh 'bundle exec rubocop'
    }
  }
)
