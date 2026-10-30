pipeline {
  agent none
  stages {
    stage('Docker') {
      agent { docker { image 'node:22' } }
      steps { sh 'npm test' }
    }
    stage('Host') {
      agent { label 'linux' }
      steps { sh 'npm test' }
    }
  }
}
