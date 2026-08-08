package island

import (
	"context"
	"log"
	"time"
)

// maxBackoff caps the escalating retry delay for a permanently-failing
// provider. Without a cap, a user who has never linked (say) their calendar
// account would otherwise retry — and log — every base interval for the
// entire life of a desktop session.
const maxBackoff = 5 * time.Minute

// Runner supervises providers. Each gets its own goroutine, and a crash or
// error in one must never affect the others or the host process.
type Runner struct {
	reg       *Registry
	providers []Provider

	// backoff is the base delay before restarting a provider that returned an
	// error or panicked. Consecutive failures escalate from this base (see
	// nextBackoff); a success resets to it. Overridden in tests.
	backoff time.Duration
	// tickEvery drives Registry.Tick, which flushes coalesced updates.
	tickEvery time.Duration

	// afterAttempt, if set, is called after each provider attempt with the
	// name, the consecutive-failure count (0 on success), and the delay
	// chosen before the next attempt. It exists solely so tests can assert on
	// the backoff sequence without sleeping through real delays; production
	// code leaves it nil.
	afterAttempt func(name string, consecutiveFailures int, delay time.Duration)
}

func NewRunner(reg *Registry, providers ...Provider) *Runner {
	return &Runner{
		reg:       reg,
		providers: providers,
		backoff:   5 * time.Second,
		tickEvery: CoalesceWindow,
	}
}

// Start spawns one goroutine per provider plus the coalescing ticker. It does
// not block.
func (rn *Runner) Start(ctx context.Context) {
	for _, p := range rn.providers {
		go rn.supervise(ctx, p)
	}
	go func() {
		t := time.NewTicker(rn.tickEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				rn.reg.Tick()
			}
		}
	}()
}

// supervise runs p, retrying with escalating backoff on error or panic, until
// ctx is cancelled.
//
// Known limitation: if p.Run ignores ctx and blocks forever (in violation of
// the Provider contract, which requires respecting cancellation), this
// goroutine blocks inside runOnce past cancellation and leaks for the rest of
// the process's life. Go has no way to force-kill a goroutine, so this is not
// fixable here — only avoidable by every provider honoring ctx.
func (rn *Runner) supervise(ctx context.Context, p Provider) {
	name := providerName(p)

	var consecutiveFailures int
	var lastLoggedDelay time.Duration

	for {
		if ctx.Err() != nil {
			return
		}
		err := rn.runOnce(ctx, p, name)
		if ctx.Err() != nil {
			return
		}

		var delay time.Duration
		if err != nil {
			consecutiveFailures++
			delay = nextBackoff(rn.backoff, consecutiveFailures)
			switch {
			case consecutiveFailures == 1:
				// First failure: log at full detail.
				log.Printf("[island] provider %s stopped: %v — retrying in %s", name, err, delay)
				lastLoggedDelay = delay
			case delay != lastLoggedDelay:
				// Only log again once the backoff actually escalates, so a
				// permanently-failing provider (e.g. an unlinked account)
				// produces a handful of lines total, not one every interval
				// forever.
				log.Printf("[island] provider %s has failed %d times, backing off to %s", name, consecutiveFailures, delay)
				lastLoggedDelay = delay
			}
		} else {
			// A successful run resets the ladder — a provider that recovers
			// must not stay slow.
			consecutiveFailures = 0
			lastLoggedDelay = 0
			delay = rn.backoff
		}

		if rn.afterAttempt != nil {
			rn.afterAttempt(name, consecutiveFailures, delay)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// nextBackoff returns the retry delay after consecutiveFailures in a row,
// doubling from base and capped at maxBackoff. consecutiveFailures <= 1
// returns base (the first retry is not yet "escalated").
func nextBackoff(base time.Duration, consecutiveFailures int) time.Duration {
	if consecutiveFailures <= 1 {
		return base
	}
	d := base
	for i := 1; i < consecutiveFailures; i++ {
		d *= 2
		if d >= maxBackoff {
			return maxBackoff
		}
	}
	return d
}

// providerName captures a provider's name once, defensively. It is called
// once per supervise loop and the result reused on every logging path — if
// Name() itself panics from a corrupted provider, that panic would otherwise
// fire *after* runOnce's recover() has already run and unwound, propagating
// and killing the process. That is exactly the failure this file exists to
// prevent, so Name() gets the same isolation as Run().
func providerName(p Provider) (name string) {
	defer func() {
		if recover() != nil {
			name = "<unknown>"
		}
	}()
	return p.Name()
}

// runOnce isolates a single provider run so a panic becomes an error instead of
// a process crash. One bad feed must not kill the island or the agent.
func (rn *Runner) runOnce(ctx context.Context, p Provider, name string) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[island] provider %s panicked: %v", name, rec)
			err = errPanicked
		}
	}()
	return p.Run(ctx, rn.reg.Upsert, rn.reg.End)
}

var errPanicked = errorString("provider panicked")

type errorString string

func (e errorString) Error() string { return string(e) }
