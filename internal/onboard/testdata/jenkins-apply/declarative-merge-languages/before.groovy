pipeline {
  agent any
  options {
    datadog(collectLogs: true, testOptimization: [enabled: false, serviceName: 'web', languages: ['RUBY'], additionalVariables: [DD_ENV: 'ci']], tags: ['team:web'])
  }
  stages {
    stage('Test') { steps { sh 'npm test' } }
  }
}
