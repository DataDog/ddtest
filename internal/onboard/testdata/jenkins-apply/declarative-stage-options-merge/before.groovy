pipeline {
  agent none
  stages {
    stage('Unit') {
      agent { label 'linux' }
      options {
        timeout(time: 10, unit: 'MINUTES')
        datadog(testOptimization: [enabled: false, serviceName: 'app', languages: ['RUBY']])
      }
      steps { sh 'npm test' }
    }
  }
}
