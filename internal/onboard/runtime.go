package onboard

type runtimeStep struct {
	ID               string            `yaml:"id"`
	ActionNode       string            `yaml:"-"`
	Number           int               `yaml:"-"`
	Source           string            `yaml:"-"`
	Parents          []string          `yaml:"-"`
	ResolutionError  string            `yaml:"-"`
	WorkingDirectory string            `yaml:"working-directory"`
	Shell            string            `yaml:"shell"`
	Env              map[string]string `yaml:"env"`
	Uses             string            `yaml:"uses"`
	Run              string            `yaml:"run"`
	If               string            `yaml:"if"`
	With             map[string]string `yaml:"with"`
}

type runtimeJob struct {
	Context   map[string]string `yaml:"-"`
	Container any               `yaml:"container"`
	RunsOn    any               `yaml:"runs-on"`
	Defaults  ciDefaults        `yaml:"defaults"`
	Env       map[string]string `yaml:"env"`
	If        string            `yaml:"if"`
	Strategy  struct {
		Matrix any `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []runtimeStep `yaml:"steps"`
}
