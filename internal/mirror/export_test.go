package mirror

// Env exposes the git environment builder to the external tests.
func (g *Git) Env(r Remote) []string { return g.env(r) }
