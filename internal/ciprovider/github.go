package ciprovider

type GitHub struct{}

func NewGitHub() *GitHub {
	return &GitHub{}
}

func (g *GitHub) Name() string {
	return "github"
}

func (g *GitHub) Configure() error {
	return nil
}
