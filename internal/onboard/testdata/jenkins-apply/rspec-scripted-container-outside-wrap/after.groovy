datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['RUBY']]) {
  node {
    stage('Test') {
      sh 'bundle exec rake spec'
    }
  }
}
node {
  stage('Package') {
    docker.image('ruby:3.3').inside {
      sh 'gem build app.gemspec'
    }
  }
}
