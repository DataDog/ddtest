pipeline {
  agent none
  options {
    datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVASCRIPT']])
  }
  stages {
    stage('Package') {
      agent { docker { image 'node:22' } }
      steps { sh 'npm pack' }
    }
    stage('Host') {
      agent { label 'linux' }
      steps { sh 'npm test' }
    }
  }
}
