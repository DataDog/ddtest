node {
  stage('Test') {
    sh 'npx vitest run'
  }
}
