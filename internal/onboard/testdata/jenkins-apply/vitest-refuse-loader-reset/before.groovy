pipeline {
  agent any
  stages {
    stage('Test') {
      steps {
        sh 'export NODE_OPTIONS=--max-old-space-size=4096; npx vitest run'
      }
    }
  }
}
