datadog(collectLogs: true) {
  node {
    stage('Test') {
      sh 'bundle exec rake spec'
    }
  }
}
