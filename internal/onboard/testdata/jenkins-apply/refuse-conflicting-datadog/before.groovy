pipeline {
  agent any
  options {
    datadog(collectLogs: true)
    datadog(testOptimization: [enabled: true, serviceName: 'web', languages: ['RUBY']])
  }
  stages {
    stage('Test') { steps { sh 'npm test' } }
  }
}
