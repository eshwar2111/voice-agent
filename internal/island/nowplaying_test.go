package island

import (
	"context"
	"errors"
	"testing"
	"time"
)

func fixedTestTime() time.Time {
	return time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
}

func TestNowPlayingActivityFields(t *testing.T) {
	clk := &fakeClock{t: fixedTestTime()}
	p := NewNowPlayingProvider(clk, nil)

	a, ok := p.activityFor(&NowPlaying{
		Track:     "Song A",
		Artist:    "Artist A",
		ArtURL:    "https://example.com/art.jpg",
		IsPlaying: true,
	})
	if !ok {
		t.Fatal("activityFor returned ok=false for a playing track")
	}
	if a.ID != "spotify.nowplaying" {
		t.Errorf("ID = %q, want spotify.nowplaying", a.ID)
	}
	if a.Data["track"] != "Song A" {
		t.Errorf("track = %v", a.Data["track"])
	}
	if a.Data["artist"] != "Artist A" {
		t.Errorf("artist = %v", a.Data["artist"])
	}
	if a.Data["art"] != "https://example.com/art.jpg" {
		t.Errorf("art = %v", a.Data["art"])
	}
}

func TestNowPlayingNoActivityWhenPausedOrEmpty(t *testing.T) {
	clk := &fakeClock{t: fixedTestTime()}
	p := NewNowPlayingProvider(clk, nil)

	cases := []*NowPlaying{
		nil,
		{Track: "Song A", Artist: "Artist A", IsPlaying: false},
		{Track: "", Artist: "Artist A", IsPlaying: true},
	}
	for _, n := range cases {
		if _, ok := p.activityFor(n); ok {
			t.Errorf("activityFor(%+v) = ok, want !ok", n)
		}
	}
}

func TestNowPlayingPollEmitsAndEnds(t *testing.T) {
	clk := &fakeClock{t: fixedTestTime()}

	var current *NowPlaying
	src := func(ctx context.Context) (*NowPlaying, error) { return current, nil }
	p := NewNowPlayingProvider(clk, src)

	var emitted []Activity
	var ended []string
	emit := func(a Activity) { emitted = append(emitted, a) }
	end := func(id string) { ended = append(ended, id) }

	// Nothing playing: no emit, no end (never went live).
	if err := p.poll(context.Background(), emit, end); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(emitted) != 0 || len(ended) != 0 {
		t.Fatalf("expected no emit/end while nothing plays, got emitted=%v ended=%v", emitted, ended)
	}

	// Track starts playing: emits.
	current = &NowPlaying{Track: "Song A", Artist: "Artist A", IsPlaying: true}
	if err := p.poll(context.Background(), emit, end); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(emitted) != 1 {
		t.Fatalf("expected 1 emit, got %d", len(emitted))
	}
	if emitted[0].Data["track"] != "Song A" {
		t.Errorf("emitted track = %v", emitted[0].Data["track"])
	}

	// Paused: ends the activity.
	current = &NowPlaying{Track: "Song A", Artist: "Artist A", IsPlaying: false}
	if err := p.poll(context.Background(), emit, end); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(ended) != 1 || ended[0] != "spotify.nowplaying" {
		t.Fatalf("expected end(spotify.nowplaying), got %v", ended)
	}
}

func TestNowPlayingPollPropagatesFetchError(t *testing.T) {
	wantErr := errors.New("spotify unavailable")
	p := NewNowPlayingProvider(&fakeClock{t: fixedTestTime()}, func(ctx context.Context) (*NowPlaying, error) {
		return nil, wantErr
	})
	err := p.poll(context.Background(), func(Activity) {}, func(string) {})
	if err != wantErr {
		t.Fatalf("poll error = %v, want %v", err, wantErr)
	}
}

func TestNowPlayingNilSourceNoop(t *testing.T) {
	p := NewNowPlayingProvider(&fakeClock{t: fixedTestTime()}, nil)
	if err := p.poll(context.Background(), func(Activity) { t.Fatal("emit called with nil source") }, func(string) { t.Fatal("end called with nil source") }); err != nil {
		t.Fatalf("poll: %v", err)
	}
}
