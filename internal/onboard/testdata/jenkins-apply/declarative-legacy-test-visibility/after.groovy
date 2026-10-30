pipeline {
  agent any
  options {
    datadog(testVisibility: [enabled: true, serviceName: 'web', languages: ['RUBY', 'JAVASCRIPT']])
  }
  stages {
    stage('Test') { steps { sh 'npm test' } }
  }
}
