package identity

import (
	"errors"
	"testing"
	"time"
)

func TestEnsureBootstrapAdminOnlyCreatesOnce(t *testing.T) {
	service, _ := newTestService(t)
	first, err := service.EnsureBootstrapAdmin(t.Context(), "admin", "admin123")
	if err != nil {
		t.Fatalf("first EnsureBootstrapAdmin: %v", err)
	}
	if first.Username != "admin" || !first.Enabled {
		t.Fatalf("first account=%+v", first)
	}

	second, err := service.EnsureBootstrapAdmin(t.Context(), "admin", "admin123")
	if err != nil {
		t.Fatalf("second EnsureBootstrapAdmin: %v", err)
	}
	if second.ID != first.ID {
		t.Fatal("second bootstrap created a different account")
	}
	if _, err := service.Login(t.Context(), "admin", "admin123", LoginMeta{}); err != nil {
		t.Fatalf("login: %v", err)
	}
}

func TestChangePasswordRevokesExistingSessions(t *testing.T) {
	service, _ := newTestService(t)
	account, err := service.EnsureBootstrapAdmin(t.Context(), "admin", "admin123")
	if err != nil {
		t.Fatal(err)
	}
	login, err := service.Login(t.Context(), "admin", "admin123", LoginMeta{ClientIP: "127.0.0.1", UserAgent: "test"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := service.Authenticate(t.Context(), login.Token); err != nil {
		t.Fatalf("Authenticate before change: %v", err)
	}
	updated, err := service.ChangePassword(t.Context(), account.ID, "admin123", "Changed123!")
	if err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if updated.Revision != account.Revision+1 {
		t.Fatalf("updated=%+v", updated)
	}
	if _, err := service.Authenticate(t.Context(), login.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old session err=%v, want unauthorized", err)
	}
	if _, err := service.ChangePassword(t.Context(), account.ID, "Changed123!", "short"); !errors.Is(err, ErrPasswordPolicy) {
		t.Fatalf("weak password err=%v", err)
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.EnsureBootstrapAdmin(t.Context(), "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	first, _ := service.Login(t.Context(), "admin", "admin123", LoginMeta{})
	second, _ := service.Login(t.Context(), "admin", "admin123", LoginMeta{})

	if err := service.Logout(t.Context(), first.Token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := service.Authenticate(t.Context(), first.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("logged-out token err=%v", err)
	}
	if _, err := service.Authenticate(t.Context(), second.Token); err != nil {
		t.Fatalf("second token after logout: %v", err)
	}
}

func TestLoginRejectsExpiredDisabledAndRateLimitedAccounts(t *testing.T) {
	service, store := newTestService(t)
	account, err := service.EnsureBootstrapAdmin(t.Context(), "admin", "admin123")
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 5; attempt++ {
		_, err := service.Login(t.Context(), "admin", "wrong-password", LoginMeta{ClientIP: "192.0.2.10"})
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d err=%v", attempt+1, err)
		}
	}
	if _, err := service.Login(t.Context(), "admin", "admin123", LoginMeta{ClientIP: "192.0.2.10"}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("rate limit err=%v", err)
	}

	service.limiter.reset()
	login, err := service.Login(t.Context(), "admin", "admin123", LoginMeta{})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	service.now = func() time.Time { return login.ExpiresAt.Add(time.Second) }
	if _, err := service.Authenticate(t.Context(), login.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired session err=%v", err)
	}

	service.now = func() time.Time { return time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC) }
	if _, err := store.db.Exec(`UPDATE accounts SET enabled=0 WHERE id=?`, account.ID); err != nil {
		t.Fatalf("disable account: %v", err)
	}
	service.limiter.reset()
	if _, err := service.Login(t.Context(), "admin", "admin123", LoginMeta{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled login err=%v", err)
	}
}

func newTestService(t *testing.T) (*Service, *Store) {
	t.Helper()
	store := newTestStore(t)
	service := NewService(store, time.Hour)
	service.now = func() time.Time { return time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC) }
	return service, store
}
