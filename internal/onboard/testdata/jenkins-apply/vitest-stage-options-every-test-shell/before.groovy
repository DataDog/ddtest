pipeline {
  agent none
  stages {
    stage('Docker') {
      agent { docker { image 'node:22' } }
      steps { sh 'npm ci' }
    }
    stage('Host') {
      agent { label 'linux' }
      steps {
        sh 'npx vitest run --dir packages/a'
        sh 'npx vitest run --dir packages/b'
      }
    }
  }
}
