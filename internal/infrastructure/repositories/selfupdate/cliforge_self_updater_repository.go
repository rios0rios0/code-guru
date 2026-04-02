package selfupdate

import cliforgeSelfupdate "github.com/rios0rios0/cliforge/pkg/selfupdate"

// CliforgeSelfUpdaterRepository implements SelfUpdaterRepository using the cliforge selfupdate library.
type CliforgeSelfUpdaterRepository struct {
	owner          string
	repo           string
	binaryName     string
	currentVersion string
}

// NewCliforgeSelfUpdaterRepository creates a new CliforgeSelfUpdaterRepository.
func NewCliforgeSelfUpdaterRepository(owner, repo, binaryName, currentVersion string) *CliforgeSelfUpdaterRepository {
	return &CliforgeSelfUpdaterRepository{
		owner:          owner,
		repo:           repo,
		binaryName:     binaryName,
		currentVersion: currentVersion,
	}
}

// Update checks for a newer version and applies the update.
func (r *CliforgeSelfUpdaterRepository) Update(dryRun, force bool) error {
	cmd := cliforgeSelfupdate.NewCommand(r.owner, r.repo, r.binaryName, r.currentVersion)
	return cmd.Execute(dryRun, force)
}
