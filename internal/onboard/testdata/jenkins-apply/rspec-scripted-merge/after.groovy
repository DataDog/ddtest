datadog(collectLogs: true, testOptimization: [enabled: true, serviceName: 'app', languages: ['RUBY']]) {
  node {
    stage('Test') {
      sh 'bundle exec rake spec'
    }
  }
}
