package claude

// SanitizeFrontmatter exposes the skill frontmatter sanitiser, so its parsing
// contract can be pinned case by case without staging an operator tree.
func SanitizeFrontmatter(content string) (string, error) {
	return sanitizeFrontmatter(content)
}

// WithBubblewrapForTest fixes what the reviewer finds when it checks for the
// bubblewrap install the subprocess environment scrub needs.
func (r *AgenticReviewerRepository) WithBubblewrapForTest(installed bool) *AgenticReviewerRepository {
	r.bubblewrapAvailable = func() bool { return installed }
	return r
}
