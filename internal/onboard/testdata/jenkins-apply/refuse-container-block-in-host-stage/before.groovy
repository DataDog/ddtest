pipeline {
  agent none
  stages {
    stage('Lint') {
      agent { docker { image 'node:22' } }
      steps { sh 'npm run lint' }
    }
    stage('Test') {
      agent { label 'linux' }
      steps {
        sh 'npm test'
        script { docker.image('node:22').inside { sh 'npm pack' } }
      }
    }
  }
}
