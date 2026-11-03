pipeline {
  agent any
  options {
    datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVASCRIPT']])
  }
  stages {
    stage('Test') { steps { sh 'npm test' } }
  }
}
