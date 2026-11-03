pipeline {
  agent any
  options {
    datadog(collectLogs: true, testOptimization: [enabled: true, serviceName: 'web', languages: ['RUBY', 'JAVASCRIPT'], additionalVariables: [DD_ENV: 'ci']], tags: ['team:web'])
  }
  stages {
    stage('Test') { steps { sh 'npm test' } }
  }
}
