pipeline {
  agent any
  options {
    datadog(testOptimization: [enabled: true, serviceName: 'web', languages: ['JAVASCRIPT'], additionalVariables: [DD_API_KEY: 'abc']])
  }
  stages {
    stage('Test') { steps { sh 'npm test' } }
  }
}
