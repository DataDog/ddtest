pipeline {
  agent { docker { image 'node:22' } }
  stages {
    stage('Test') {
      steps {
        sh 'npm ci'
        sh 'npm test -- packages/a'
        sh 'npm test -- packages/b'
      }
    }
  }
}
