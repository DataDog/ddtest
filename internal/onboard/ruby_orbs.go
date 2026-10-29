// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

// CircleCI's Ruby orb, modeled from its source (CircleCI-Public/ruby-orb)
// for its 1.x to 4.x releases: install selects the Ruby version
// (ddtest-runtime), install-deps runs bundle install in app-dir, rspec-test
// runs RSpec as its script does on each parallel container's share of the
// test files, and rubocop-check only lints. 4.x's rspec-test runs a
// CircleCI test suite ddtest cannot read, so it stays unresolved.
func (rubyDiscovery) ciComponents(provider string) []ciComponent {
	if provider != circleCIProvider {
		return nil
	}
	return []ciComponent{
		{Name: "circleci/ruby", Majors: []string{"1"}, Definition: rubyOrbModel + rubyOrbRSpecV1},
		{Name: "circleci/ruby", Majors: []string{"2"}, Definition: rubyOrbModel + rubyOrbRSpec("")},
		{Name: "circleci/ruby", Majors: []string{"3"}, Definition: rubyOrbModel + rubyOrbRSpec("      - ddtest-review: ruby/rspec-test (3.x) runs RSpec with --seed $RANDOM; local validation runs it with the suite's configured order.\n")},
		{Name: "circleci/ruby", Majors: []string{"4"}, Definition: rubyOrbModel},
	}
}

// rubyOrbModel is what every modeled release shares: the default executor,
// install, install-deps and rubocop-check.
const rubyOrbModel = `executors:
  default:
    parameters:
      tag:
        type: string
        default: "3.3"
    docker:
      - image: cimg/ruby:<< parameters.tag >>
commands:
  install:
    parameters:
      version:
        type: string
    steps:
      - ddtest-runtime:
          ruby: << parameters.version >>
  install-deps:
    parameters:
      app-dir:
        type: string
        default: "."
      gemfile:
        type: string
        default: Gemfile
      pre-install-steps:
        type: steps
        default: []
    steps:
      - steps: << parameters.pre-install-steps >>
      - when:
          condition:
            equal: [Gemfile, << parameters.gemfile >>]
          steps:
            - run:
                working_directory: << parameters.app-dir >>
                command: bundle install
      - unless:
          condition:
            equal: [Gemfile, << parameters.gemfile >>]
          steps:
            - ddtest-unresolved: ruby/install-deps configures Bundler to use << parameters.gemfile >> in later steps, which ddtest does not follow; set BUNDLE_GEMFILE in the job's environment instead
  rubocop-check:
    steps:
      - ddtest-review: ruby/rubocop-check runs RuboCop, a linter outside the test path.
`

// rubyOrbRSpecV1 is 1.x's rspec-test: always --order.
const rubyOrbRSpecV1 = `  rspec-test:
    parameters:
      include:
        type: string
        default: spec/**/*_spec.rb
      out-path:
        type: string
        default: /tmp/test-results/rspec
      app-dir:
        type: string
        default: "."
      order:
        type: string
        default: defined
    steps:
` + rubyOrbInclude + `            - run:
                working_directory: << parameters.app-dir >>
                command: bundle exec rspec --profile 10 --format RspecJunitFormatter --out << parameters.out-path >>/results.xml --format progress --order << parameters.order >>
`

// rubyOrbInclude splits the default include, which RSpec's own default
// pattern matches, and leaves any other include unresolved: without the
// split file list the command would run other files.
const rubyOrbInclude = `      - ddtest-review: ruby/rspec-test splits << parameters.include >> across parallel containers by timing; local validation runs RSpec once on every test.
      - unless:
          condition:
            equal: [spec/**/*_spec.rb, << parameters.include >>]
          steps:
            - ddtest-unresolved: ruby/rspec-test runs the files matching << parameters.include >>, which ddtest does not map to an RSpec command
      - when:
          condition:
            equal: [spec/**/*_spec.rb, << parameters.include >>]
          steps:
`

// rubyOrbRSpec is 2.x and 3.x's rspec-test: --order and --tag only when
// set. A tag list (3.x splits tags on spaces) stays unresolved.
func rubyOrbRSpec(extra string) string {
	command := "bundle exec rspec --profile 10 --format RspecJunitFormatter --out << parameters.out-path >>/results.xml --format progress"
	variant := func(order, tag bool) string {
		condition := func(set bool, name string) string {
			if set {
				return "                  - not:\n                      equal: [\"\", << parameters." + name + " >>]\n"
			}
			return "                  - equal: [\"\", << parameters." + name + " >>]\n"
		}
		text := "            - when:\n                condition:\n                  and:\n" + condition(order, "order") + condition(tag, "tag") + "                steps:\n                  - run:\n                      working_directory: << parameters.app-dir >>\n                      command: " + command
		if order {
			text += " --order << parameters.order >>"
		}
		if tag {
			text += " --tag << parameters.tag >>"
		}
		return text + "\n"
	}
	return `  rspec-test:
    parameters:
      include:
        type: string
        default: spec/**/*_spec.rb
      out-path:
        type: string
        default: /tmp/test-results/rspec
      app-dir:
        type: string
        default: "."
      order:
        type: string
        default: ""
      tag:
        type: string
        default: ""
    steps:
` + extra + `      - when:
          condition:
            matches:
              pattern: ".* .*"
              value: << parameters.tag >>
          steps:
            - ddtest-unresolved: ruby/rspec-test passes each tag of << parameters.tag >> separately, which ddtest does not map to an RSpec command
` + rubyOrbInclude + variant(false, false) + variant(true, false) + variant(false, true) + variant(true, true)
}
