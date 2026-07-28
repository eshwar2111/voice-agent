package trust

type LadderRecoverer struct {
	MaxRetries int
	replanned  bool
}

func NewLadderRecoverer(maxRetries int) *LadderRecoverer {
	return &LadderRecoverer{MaxRetries: maxRetries}
}

// MarkReplanned records that the one allowed re-plan has been consumed.
func (r *LadderRecoverer) MarkReplanned() { r.replanned = true }

// Recover chooses the next move. attempt = number of executions already made
// for this step (1 after the first run).
func (r *LadderRecoverer) Recover(step Step, attempt int, lastErr error) Decision {
	if attempt <= r.MaxRetries {
		return Retry
	}
	if !r.replanned {
		return Replan
	}
	return Ask
}
