package entities

// ReviewWorkspace is a disposable local checkout of a pull request, prepared
// for reviewers that read the repository instead of only the diff text. The
// checkout sits at the pull request's source head; `BaseCommit` is the
// merge-base with the target branch, so `git diff <BaseCommit>...HEAD`
// inside `Path` reproduces exactly what the hosting provider shows as the
// pull request's changes.
type ReviewWorkspace struct {
	// Root is the directory that owns everything the workspace created. It
	// is what gets removed on release, so it must never be a directory the
	// workspace did not create itself.
	Root string

	// Path is the repository checkout, and the working directory the
	// reviewer runs in. Always inside Root.
	Path string

	// SourceBranch and TargetBranch are the branch names the checkout was
	// built from (no `refs/heads/` prefix).
	SourceBranch string
	TargetBranch string

	// BaseCommit is the merge-base of the target and source branches — the
	// left side of the pull request's diff.
	BaseCommit string

	// HeadCommit is the source commit that was checked out and reviewed.
	HeadCommit string

	// ChangedFiles lists the files the pull request changes, as computed
	// locally from BaseCommit to HeadCommit.
	ChangedFiles []WorkspaceFileChange
}

// WorkspaceFileChange is one file of a pull request as seen in the local
// checkout.
type WorkspaceFileChange struct {
	// Path is the file's path in the head commit (or in the base commit,
	// for a deletion), relative to the repository root.
	Path string

	// Status is git's single-letter change status: `A` added, `M`
	// modified, `D` deleted, `R` renamed, `C` copied, `T` type-changed.
	Status string

	// Additions and Deletions are the changed line counts. Both are zero
	// for a binary file, which Binary flags explicitly.
	Additions int
	Deletions int
	Binary    bool
}
