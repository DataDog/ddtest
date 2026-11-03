pipeline {
  agent any
  stages {
    stage('Unit') { steps { sh 'npm test' } }
    stage('Browser') {
      steps {
        sh '''
          npm ci
          npm test -- --project browser
        '''
      }
    }
  }
}
