package island

import (
	"context"
	"log"
	"time"
)

// Runner supervises providers. Each gets its own goroutine, and a crash or
// error in one must never affect the others or the host process.
type Runner struct {
	reg       *Registry
	providers []Provider

	// backoff is the delay before restarting a provider that returned an error
	// or panicked. Overridden in tests.
	backoff time.Duration
	// tickEvery drives Registry.Tick, which flushes coalesced updates.
	tickEvery time.Duration
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

func (rn *Runner) supervise(ctx context.Context, p Provider) {
	for {
		if ctx.Err() != nil {
			return
		}
		err := rn.runOnce(ctx, p)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("[island] provider %s stopped: %v — retrying in %s", p.Name(), err, rn.backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(rn.backoff):
		}
	}
}

// runOnce isolates a single provider run so a panic becomes an error instead of
// a process crash. One bad feed must not kill the island or the agent.
func (rn *Runner) runOnce(ctx context.Context, p Provider) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[island] provider %s panicked: %v", p.Name(), rec)
			err = errPanicked
		}
	}()
	return p.Run(ctx, rn.reg.Upsert, rn.reg.End)
}

var errPanicked = errorString("provider panicked")

type errorString string

func (e errorString) Error() string { return string(e) }
