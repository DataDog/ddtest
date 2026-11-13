properties([disableConcurrentBuilds()])

timestamps {
  datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['RUBY']]) {
    node('ruby') {
      stage('Test') {
        sh '''
bundle install
bundle exec rake spec
'''
      }
    }
  }
}

node('deploy') {
  stage('Deploy') {
    sh 'kubectl apply -f deploy/'
  }
}
