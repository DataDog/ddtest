pipeline {
  agent any
  stages {
    stage('Build') {
      steps { sh 'npm ci' }
    }
    stage('Checks') {
      parallel {
        stage('Unit') {
          environment { CI = 'true' }
          steps {
            sh 'npm test'
          }
        }
        stage('Package') {
          agent { docker { image 'node:22' } }
          steps { sh 'npm pack' }
        }
      }
    }
    stage('Integration') {
      steps { sh 'npm test -- --selectProjects integration' }
    }
  }
}
