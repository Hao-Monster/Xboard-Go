package schedule

import (
	"testing"
	"time"
)

func TestDailyWindowCrossMidnight(t *testing.T) {
	location := mustLocation(t)
	window, err := NewDailyWindow(location, "19:00", "01:00")
	if err != nil {
		t.Fatalf("NewDailyWindow() error = %v", err)
	}

	tests := []struct {
		name       string
		now        time.Time
		enabled    bool
		next       time.Time
		nextTarget bool
	}{
		{
			name:       "before enable",
			now:        localTime(location, 2026, 8, 20, 12, 0),
			enabled:    false,
			next:       localTime(location, 2026, 8, 20, 19, 0),
			nextTarget: true,
		},
		{
			name:       "inside evening window",
			now:        localTime(location, 2026, 8, 20, 20, 0),
			enabled:    true,
			next:       localTime(location, 2026, 8, 21, 1, 0),
			nextTarget: false,
		},
		{
			name:       "inside after-midnight window",
			now:        localTime(location, 2026, 8, 21, 0, 30),
			enabled:    true,
			next:       localTime(location, 2026, 8, 21, 1, 0),
			nextTarget: false,
		},
		{
			name:       "disable boundary is exclusive",
			now:        localTime(location, 2026, 8, 21, 1, 0),
			enabled:    false,
			next:       localTime(location, 2026, 8, 21, 19, 0),
			nextTarget: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := window.StateAt(tt.now)
			if state.Enabled != tt.enabled {
				t.Fatalf("Enabled = %v, want %v", state.Enabled, tt.enabled)
			}
			if !state.NextTransition.Equal(tt.next) {
				t.Fatalf("NextTransition = %s, want %s", state.NextTransition, tt.next)
			}
			if state.NextTargetEnabled != tt.nextTarget {
				t.Fatalf("NextTargetEnabled = %v, want %v", state.NextTargetEnabled, tt.nextTarget)
			}
		})
	}
}

func TestDailyWindowSameDay(t *testing.T) {
	location := mustLocation(t)
	window, err := NewDailyWindow(location, "09:30", "18:15")
	if err != nil {
		t.Fatalf("NewDailyWindow() error = %v", err)
	}

	inside := window.StateAt(localTime(location, 2026, 8, 20, 12, 0))
	if !inside.Enabled {
		t.Fatal("inside same-day window should be enabled")
	}
	if want := localTime(location, 2026, 8, 20, 18, 15); !inside.NextTransition.Equal(want) {
		t.Fatalf("NextTransition = %s, want %s", inside.NextTransition, want)
	}

	after := window.StateAt(localTime(location, 2026, 8, 20, 19, 0))
	if after.Enabled {
		t.Fatal("after same-day window should be disabled")
	}
	if want := localTime(location, 2026, 8, 21, 9, 30); !after.NextTransition.Equal(want) {
		t.Fatalf("NextTransition = %s, want %s", after.NextTransition, want)
	}
}

func TestDailyWindowRejectsInvalidTimes(t *testing.T) {
	location := mustLocation(t)
	for _, input := range []struct {
		enable  string
		disable string
	}{
		{enable: "19:00", disable: "19:00"},
		{enable: "24:00", disable: "01:00"},
		{enable: "9:00", disable: "10:00"},
		{enable: "09:00", disable: ""},
	} {
		if _, err := NewDailyWindow(location, input.enable, input.disable); err == nil {
			t.Fatalf("NewDailyWindow(%q, %q) expected an error", input.enable, input.disable)
		}
	}
}

func mustLocation(t *testing.T) *time.Location {
	t.Helper()
	location, err := time.LoadLocation("Asia/Singapore")
	if err != nil {
		t.Fatalf("load timezone: %v", err)
	}
	return location
}

func localTime(location *time.Location, year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, location)
}

func BenchmarkDailyWindowStateAt(b *testing.B) {
	location, err := time.LoadLocation("Asia/Singapore")
	if err != nil {
		b.Fatal(err)
	}
	window, err := NewDailyWindow(location, "19:00", "01:00")
	if err != nil {
		b.Fatal(err)
	}
	now := time.Date(2026, 8, 20, 20, 0, 0, 0, location)
	b.ReportAllocs()
	for b.Loop() {
		_ = window.StateAt(now)
	}
}
