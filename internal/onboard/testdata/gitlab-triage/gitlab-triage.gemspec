# frozen_string_literal: true

Gem::Specification.new do |spec|
  spec.name = 'gitlab-triage'
  spec.version = '1.0.0'
  spec.summary = 'Fixture modeled on gitlab-org/ruby/gems/gitlab-triage'
  spec.authors = ['ddtest']
  spec.files = `git ls-files -z`.split("\x0")
  spec.required_ruby_version = '>= 3.0'

  spec.add_development_dependency 'rake', '~> 13.2'
  spec.add_development_dependency 'rspec', '~> 3.8'
end
