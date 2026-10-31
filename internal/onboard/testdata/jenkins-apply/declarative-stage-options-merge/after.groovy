pipeline {
  agent none
  stages {
    stage('Unit') {
      agent { label 'linux' }
      options {
        timeout(time: 10, unit: 'MINUTES')
        datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['RUBY', 'JAVASCRIPT']])
      }
      steps { sh 'npm test' }
    }
  }
}
