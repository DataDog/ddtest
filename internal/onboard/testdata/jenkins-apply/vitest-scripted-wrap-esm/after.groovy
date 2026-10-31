datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVASCRIPT']]) {
  node {
    stage('Test') {
      sh 'if [ -n "${DD_TRACE_PATH:-}" ]; then export NODE_OPTIONS="${NODE_OPTIONS:-} --import $DD_TRACE_PATH/register.js"; fi; npx vitest run'
    }
  }
}
