pipeline {
  agent any
  stages {
    stage('Test') {
      steps {
        sh 'npm ci'
        sh 'npx vitest run --dir packages/a'
        sh 'npx vitest run --dir packages/b'
      }
    }
  }
}
