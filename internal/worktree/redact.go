package worktree

// SummarizeReason is the remote snapshot's reason hygiene, exported for the
// one other part of that snapshot that records a reason: the open-PR list
// internal/remote reads through gh (ADR 0046 §2.1). It makes s one bounded
// line with any credential a URL carries replaced, exactly as the git parts'
// reasons are, so the two cannot disagree on what is redacted.
func SummarizeReason(s string) string { return summarizeReason(s) }
