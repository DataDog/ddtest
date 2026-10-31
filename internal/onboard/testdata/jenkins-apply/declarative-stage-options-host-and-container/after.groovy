pipeline {
  agent none
  stages {
    stage('Docker') {
      agent { docker { image 'node:22' } }
      steps { sh 'npm test' }
    }
    stage('Host') {
      agent { label 'linux' }
      options {
        datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVASCRIPT']])
      }
      steps { sh 'npm test' }
    }
  }
}
