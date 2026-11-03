node {
  stage('Test') {
    sh 'bundle exec rake spec'
  }
  stage('Package') {
    docker.image('ruby:3.3').inside {
      sh 'gem build app.gemspec'
    }
  }
}
