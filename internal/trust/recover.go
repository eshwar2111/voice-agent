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

// Reset clears the once-per-plan re-plan guard. TrustedExecutor.Run calls this
// at the start of every plan so the "re-plan at most once" budget is per-plan,
// not per-process (the executor holds one shared recoverer for all plans).
func (r *LadderRecoverer) Reset() { r.replanned = false }

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
