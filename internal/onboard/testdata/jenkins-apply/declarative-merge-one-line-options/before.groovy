pipeline {
  agent any
  options { timestamps() }
  stages {
    stage('Test') { steps { sh 'npm test' } }
  }
}
