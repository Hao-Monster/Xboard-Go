package maintenance

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestCleanupExpiredReportsBoundedExistingPolicies(t *testing.T) {
	now := time.Date(2026, 8, 25, 14, 0, 0, 0, time.UTC)
	store := &fakeCleanupStore{results: map[string]int64{
		"tickets": 1, "registration_ip": 2, "password_resets": 3,
		"registration_email": 4, "login_links": 5, "login_failures": 6,
	}}

	result, err := CleanupExpired(context.Background(), store, now, 37)
	if err != nil {
		t.Fatalf("CleanupExpired() error = %v", err)
	}
	want := CleanupResult{
		StaleTicketsClosed: 1, RegistrationIPLimitsPruned: 2, PasswordResetsPruned: 3,
		RegistrationEmailVerificationsPruned: 4, LoginLinksPruned: 5, LoginFailureLimitsPruned: 6,
	}
	if result != want {
		t.Fatalf("CleanupExpired() = %#v, want %#v", result, want)
	}
	wantCalls := []string{"tickets", "registration_ip", "password_resets", "registration_email", "login_links", "login_failures"}
	if !reflect.DeepEqual(store.calls, wantCalls) {
		t.Fatalf("cleanup calls = %#v, want %#v", store.calls, wantCalls)
	}
	if !store.ticketCutoff.Equal(now.Add(-24*time.Hour)) || !store.ticketNow.Equal(now) || store.limit != 37 {
		t.Fatalf("cleanup boundaries = cutoff %s now %s limit %d", store.ticketCutoff, store.ticketNow, store.limit)
	}
}

func TestCleanupExpiredRejectsInvalidInputWithoutWrites(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		now   time.Time
		limit int
	}{
		{name: "zero time", limit: 1},
		{name: "zero limit", now: time.Now(), limit: 0},
		{name: "excessive limit", now: time.Now(), limit: 1_001},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store := &fakeCleanupStore{}
			if _, err := CleanupExpired(context.Background(), store, testCase.now, testCase.limit); err == nil {
				t.Fatal("CleanupExpired() accepted invalid input")
			}
			if len(store.calls) != 0 {
				t.Fatalf("invalid cleanup performed calls: %#v", store.calls)
			}
		})
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	store := &fakeCleanupStore{}
	if _, err := CleanupExpired(cancelled, store, time.Now(), 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("CleanupExpired(cancelled) error = %v", err)
	}
	if len(store.calls) != 0 {
		t.Fatalf("cancelled cleanup performed calls: %#v", store.calls)
	}
}

func TestCleanupExpiredStopsAndNamesTheFailedStage(t *testing.T) {
	store := &fakeCleanupStore{failAt: "registration_email"}
	result, err := CleanupExpired(context.Background(), store, time.Now(), 100)
	if err == nil || !errors.Is(err, errCleanupTest) {
		t.Fatalf("CleanupExpired() error = %v", err)
	}
	if result.PasswordResetsPruned != 0 || result.RegistrationEmailVerificationsPruned != 0 {
		t.Fatalf("failed cleanup returned partial success: %#v", result)
	}
	wantCalls := []string{"tickets", "registration_ip", "password_resets", "registration_email"}
	if !reflect.DeepEqual(store.calls, wantCalls) {
		t.Fatalf("cleanup calls = %#v, want %#v", store.calls, wantCalls)
	}
}

var errCleanupTest = errors.New("cleanup test failure")

type fakeCleanupStore struct {
	results      map[string]int64
	failAt       string
	calls        []string
	limit        int
	ticketCutoff time.Time
	ticketNow    time.Time
}

func (s *fakeCleanupStore) cleanup(name string, limit int) (int64, error) {
	s.calls = append(s.calls, name)
	s.limit = limit
	if s.failAt == name {
		return 0, errCleanupTest
	}
	return s.results[name], nil
}

func (s *fakeCleanupStore) CloseStaleAnsweredTickets(_ context.Context, cutoff, now time.Time, limit int) (int64, error) {
	s.ticketCutoff, s.ticketNow = cutoff, now
	return s.cleanup("tickets", limit)
}

func (s *fakeCleanupStore) PruneExpiredRegistrationIPLimits(_ context.Context, _ time.Time, limit int) (int64, error) {
	return s.cleanup("registration_ip", limit)
}

func (s *fakeCleanupStore) PruneExpiredPasswordResets(_ context.Context, _ time.Time, limit int) (int64, error) {
	return s.cleanup("password_resets", limit)
}

func (s *fakeCleanupStore) PruneExpiredRegistrationEmailVerifications(_ context.Context, _ time.Time, limit int) (int64, error) {
	return s.cleanup("registration_email", limit)
}

func (s *fakeCleanupStore) PruneExpiredLoginLinks(_ context.Context, _ time.Time, limit int) (int64, error) {
	return s.cleanup("login_links", limit)
}

func (s *fakeCleanupStore) PruneExpiredLoginFailureLimits(_ context.Context, _ time.Time, limit int) (int64, error) {
	return s.cleanup("login_failures", limit)
}
