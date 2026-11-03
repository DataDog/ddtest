def dd = [enabled: true, serviceName: 'web', languages: ['RUBY']]
pipeline {
  agent any
  options {
    datadog(testOptimization: dd)
  }
  stages {
    stage('Test') { steps { sh 'npm test' } }
  }
}
