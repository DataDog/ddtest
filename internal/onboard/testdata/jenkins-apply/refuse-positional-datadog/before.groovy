datadog([collectLogs: true]) {
  node {
    stage('Test') {
      sh 'npm test'
    }
  }
}
