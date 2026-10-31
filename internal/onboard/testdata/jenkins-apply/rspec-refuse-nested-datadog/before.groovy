datadog(collectLogs: true) {
  datadog(testOptimization: [enabled: true, serviceName: 'web', languages: ['JAVASCRIPT']]) {
    node {
      sh 'bundle exec rake spec'
    }
  }
}
