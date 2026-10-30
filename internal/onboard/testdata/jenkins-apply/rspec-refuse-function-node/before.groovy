def integration() {
  node('db') {
    sh 'bundle exec rake spec:integration'
  }
}

node {
  stage('Test') {
    sh 'bundle exec rake spec'
  }
}
integration()
