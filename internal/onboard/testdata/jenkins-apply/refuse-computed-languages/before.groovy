pipeline {
  agent any
  options {
    datadog(testOptimization: [enabled: true, serviceName: 'web', languages: params.LANGUAGES])
  }
  stages {
    stage('Test') { steps { sh 'npm test' } }
  }
}
