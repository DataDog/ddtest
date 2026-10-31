pipeline {
  agent { docker { image 'node:22' } }
  stages {
    stage('Test') { steps { sh 'npm test' } }
  }
}
