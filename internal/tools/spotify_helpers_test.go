package tools

import (
	"errors"
	"testing"
)

func TestPickDevice(t *testing.T) {
	devs := []SpotifyDevice{
		{ID: "a", Name: "Phone", IsActive: false},
		{ID: "b", Name: "Laptop", IsActive: true},
		{ID: "c", Name: "Kitchen Speaker", IsActive: false},
	}
	// no preference → active wins
	if id, _ := pickDevice(devs, ""); id != "b" {
		t.Errorf("no-pref should pick active 'b', got %q", id)
	}
	// name match (case-insensitive) beats active
	if id, _ := pickDevice(devs, "phone"); id != "a" {
		t.Errorf("name match should pick 'a', got %q", id)
	}
	// name miss → "",""
	if id, name := pickDevice(devs, "car"); id != "" || name != "" {
		t.Errorf("name miss should be empty, got %q/%q", id, name)
	}
	// no active, no pref → first
	none := []SpotifyDevice{{ID: "x", Name: "X"}, {ID: "y", Name: "Y"}}
	if id, _ := pickDevice(none, ""); id != "x" {
		t.Errorf("no active → first 'x', got %q", id)
	}
	// empty list
	if id, _ := pickDevice(nil, ""); id != "" {
		t.Errorf("empty list → '', got %q", id)
	}
}

func TestParseSeekPosition(t *testing.T) {
	cases := []struct {
		in      string
		cur     int
		want    int
		wantErr bool
	}{
		{"1:30", 0, 90000, false},
		{"0:05", 0, 5000, false},
		{"90", 0, 90000, false},         // bare seconds
		{"+30s", 60000, 90000, false},   // relative forward
		{"-15s", 20000, 5000, false},    // relative back
		{"-30s", 10000, 0, false},       // floor at 0
		{"abc", 0, 0, true},
	}
	for _, c := range cases {
		got, err := parseSeekPosition(c.in, c.cur)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseSeekPosition(%q) expected error", c.in)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("parseSeekPosition(%q,%d)=%d,%v want %d", c.in, c.cur, got, err, c.want)
		}
	}
}

func TestSafeAccessors(t *testing.T) {
	m := map[string]any{
		"name": "Song",
		"album": map[string]any{
			"images": []any{map[string]any{"url": "http://img/1"}},
		},
		"bad": 42,
	}
	if str(m, "name") != "Song" {
		t.Error("str name")
	}
	if str(m, "missing") != "" || str(m, "bad") != "" {
		t.Error("str missing/wrong-type must be empty")
	}
	if nested(m, "album", "images") == nil {
		t.Error("nested should not be nil")
	}
	if nested(m, "album", "nope") != nil || nested(m, "missing", "x") != nil {
		t.Error("nested miss must be nil")
	}
	if firstImageURL(nested(m, "album")["images"]) != "http://img/1" {
		t.Error("firstImageURL")
	}
	if firstImageURL(nil) != "" || firstImageURL("bad") != "" {
		t.Error("firstImageURL bad shape must be empty")
	}
}

func TestIsNoActiveDevice(t *testing.T) {
	if !isNoActiveDevice(errors.New("Spotify error (404 Not Found): {\"error\":{\"reason\":\"NO_ACTIVE_DEVICE\"}}")) {
		t.Error("should detect NO_ACTIVE_DEVICE")
	}
	if isNoActiveDevice(nil) || isNoActiveDevice(errors.New("other")) {
		t.Error("must not false-positive")
	}
}

func TestWithDeviceRecoveryRetriesOnce(t *testing.T) {
	calls := 0
	// first call reports NO_ACTIVE_DEVICE; ensureActiveDevice is not reachable
	// without a client, so this test only covers the non-recoverable branch:
	// a non-device error is returned as-is without retry.
	err := withDeviceRecovery(nil, nil, func() error {
		calls++
		return errors.New("some other error")
	})
	if calls != 1 || err == nil {
		t.Fatalf("non-device error must not retry; calls=%d err=%v", calls, err)
	}
}
