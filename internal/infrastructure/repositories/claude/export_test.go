package claude

// SanitizeFrontmatter exposes the skill frontmatter sanitiser, so its parsing
// contract can be pinned case by case without staging an operator tree.
func SanitizeFrontmatter(content string) (string, error) {
	return sanitizeFrontmatter(content)
}
