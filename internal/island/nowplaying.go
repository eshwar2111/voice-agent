package island

import (
	"context"
	"time"
)

// pollInterval is short enough that the "now playing" pill feels live
// (track/pause changes show up quickly) without hammering the Spotify API
// every tick.
const pollInterval = 4 * time.Second

// NowPlaying is the currently playing Spotify track, as needed by the island.
type NowPlaying struct {
	Track     string
	Artist    string
	ArtURL    string
	IsPlaying bool
}

// NowPlayingSource fetches the current Spotify playback state. Injected so
// internal/island stays stdlib-only (no import of internal/tools or
// internal/auth, which would cycle through internal/tools -> internal/island
// if that package ever depended back on island) and the provider is testable
// without network access.
//
// Contract, mirroring MeetingSource: (nil, nil) means "nothing to show" —
// Spotify not linked, or the account genuinely has no playback state. That is
// the common steady state for most users, not a failure. A non-nil error is a
// genuine fetch failure (network, auth refresh, quota) and drives the
// runner's backoff.
type NowPlayingSource func(ctx context.Context) (*NowPlaying, error)

// NowPlayingProvider polls Spotify's currently-playing endpoint and surfaces
// it as a live activity. Modeled directly on MeetingProvider.
type NowPlayingProvider struct {
	clock Clock
	src   NowPlayingSource

	// live tracks whether the last poll produced a visible activity, so Run
	// knows when to call end().
	live bool
}

func NewNowPlayingProvider(clock Clock, src NowPlayingSource) *NowPlayingProvider {
	return &NowPlayingProvider{clock: clock, src: src}
}

func (p *NowPlayingProvider) Name() string { return "spotify.nowplaying" }

func (p *NowPlayingProvider) activityFor(n *NowPlaying) (Activity, bool) {
	if n == nil || !n.IsPlaying || n.Track == "" {
		return Activity{}, false
	}
	return Activity{
		ID:       "spotify.nowplaying",
		Kind:     "spotify.nowplaying",
		Priority: 20,
		Data: map[string]any{
			// Field names must match internal/ui/assets/js/activities.js's
			// spotify.nowplaying renderer (leading/compact/expanded).
			"track":  n.Track,
			"artist": n.Artist,
			"art":    n.ArtURL,
		},
		// Open-ended: a track has no fixed end the island should count down to
		// (unlike a meeting or timer), so Started/Ends stay zero.
		Significant: false,
	}, true
}

// poll fetches the current track and emits/ends as appropriate. Factored out
// of Run as a method, mirroring MeetingProvider.poll, so tests can drive it
// directly without waiting on a real ticker.
func (p *NowPlayingProvider) poll(ctx context.Context, emit func(Activity), end func(string)) error {
	if p.src == nil {
		return nil
	}
	n, err := p.src(ctx)
	if err != nil {
		return err
	}
	a, ok := p.activityFor(n)
	if ok {
		emit(a)
		p.live = true
	} else if p.live {
		end("spotify.nowplaying")
		p.live = false
	}
	return nil
}

// Run polls roughly every 4 seconds.
func (p *NowPlayingProvider) Run(ctx context.Context, emit func(Activity), end func(string)) error {
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	if err := p.poll(ctx, emit, end); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			if err := p.poll(ctx, emit, end); err != nil {
				return err
			}
		}
	}
}
