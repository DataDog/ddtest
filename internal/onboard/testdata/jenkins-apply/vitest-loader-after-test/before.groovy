pipeline {
  agent any
  options {
    datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVASCRIPT']])
  }
  stages {
    stage('Test') {
      steps {
        sh 'npx vitest run; if [ -n "${DD_TRACE_PATH:-}" ]; then export NODE_OPTIONS="${NODE_OPTIONS:-} --import $DD_TRACE_PATH/register.js"; fi'
      }
    }
  }
}
