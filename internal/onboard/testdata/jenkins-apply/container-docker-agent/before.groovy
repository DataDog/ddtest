pipeline {
  agent { docker { image 'node:22' } }
  stages {
    stage('Install') {
      steps {
        sh 'npm ci'
      }
    }
    stage('Test') {
      steps {
        sh '''
          export CI=true
          npm test
        '''
      }
    }
  }
}
