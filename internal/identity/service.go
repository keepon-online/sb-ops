package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	defaultSessionTTL  = 24 * time.Hour
	loginFailureLimit  = 5
	loginFailureWindow = 15 * time.Minute
)

type LoginMeta struct {
	ClientIP  string
	UserAgent string
}

type LoginResult struct {
	Token     string
	Account   Account
	ExpiresAt time.Time
}

type Principal struct {
	AccountID string
	SessionID string
	Username  string
}

type Service struct {
	store      *Store
	sessionTTL time.Duration
	now        func() time.Time
	limiter    *loginLimiter
	dummyHash  []byte
}

func NewService(store *Store, sessionTTL time.Duration) *Service {
	if sessionTTL <= 0 {
		sessionTTL = defaultSessionTTL
	}
	dummyHash, _ := bcrypt.GenerateFromPassword([]byte("invalid-password-value"), bcrypt.DefaultCost)
	return &Service{
		store: store, sessionTTL: sessionTTL,
		now:       func() time.Time { return time.Now().UTC() },
		limiter:   newLoginLimiter(loginFailureLimit, loginFailureWindow),
		dummyHash: dummyHash,
	}
}

// EnsureBootstrapAdmin seeds a default administrator when no account exists.
// It is used by test fixtures; production initialization goes through
// InitializeAdmin via the setup endpoint.
func (s *Service) EnsureBootstrapAdmin(ctx context.Context, username, password string) (Account, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		username = "admin"
	}
	count, err := s.store.AccountCount(ctx)
	if err != nil {
		return Account{}, err
	}
	if count > 0 {
		account, err := s.store.AccountByUsername(ctx, username)
		if errors.Is(err, sql.ErrNoRows) {
			return Account{}, ErrBootstrapAdminAbsent
		}
		return account, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return Account{}, err
	}
	now := s.now().UTC()
	account := Account{
		ID: uuid.NewString(), Username: username, PasswordHash: hash, Enabled: true,
		Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.CreateAccount(ctx, account); err != nil {
		if existing, findErr := s.store.AccountByUsername(ctx, username); findErr == nil {
			return existing, nil
		}
		return Account{}, err
	}
	return account, nil
}

func (s *Service) InitializeAdmin(ctx context.Context, username, password string) (Account, error) {
	username = strings.TrimSpace(username)
	if username == "" || !validPassword(password) {
		return Account{}, ErrInvalidAccountInput
	}
	initialized, err := s.store.Initialized(ctx)
	if err != nil {
		return Account{}, err
	}
	if initialized {
		return Account{}, ErrAlreadyInitialized
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return Account{}, err
	}
	now := s.now().UTC()
	account := Account{
		ID: uuid.NewString(), Username: username, PasswordHash: hash, Enabled: true,
		Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.InitializeAdmin(ctx, account); err != nil {
		return Account{}, err
	}
	return account, nil
}

func (s *Service) Login(ctx context.Context, username, password string, meta LoginMeta) (LoginResult, error) {
	username = strings.TrimSpace(username)
	accountKey := "account:" + strings.ToLower(username)
	ipKey := "ip:" + strings.TrimSpace(meta.ClientIP)
	now := s.now().UTC()
	if s.limiter.blocked(accountKey, now) || s.limiter.blocked(ipKey, now) {
		return LoginResult{}, ErrRateLimited
	}

	account, err := s.store.AccountByUsername(ctx, username)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return LoginResult{}, err
	}
	hash := s.dummyHash
	if err == nil {
		hash = account.PasswordHash
	}
	passwordMatches := bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
	if err != nil || !account.Enabled || !passwordMatches {
		s.limiter.failure(accountKey, now)
		s.limiter.failure(ipKey, now)
		return LoginResult{}, ErrInvalidCredentials
	}
	s.limiter.success(accountKey)
	s.limiter.success(ipKey)

	token, err := randomToken()
	if err != nil {
		return LoginResult{}, err
	}
	expiresAt := now.Add(s.sessionTTL)
	if err := s.store.SaveSession(ctx, Session{
		ID: uuid.NewString(), AccountID: account.ID, TokenHash: hashToken(token),
		ClientIP: strings.TrimSpace(meta.ClientIP), UserAgent: strings.TrimSpace(meta.UserAgent),
		ExpiresAt: expiresAt, CreatedAt: now,
	}); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Token: token, Account: account, ExpiresAt: expiresAt}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Principal{}, ErrUnauthorized
	}
	session, err := s.store.SessionByTokenHash(ctx, hashToken(token))
	if err != nil || session.RevokedAt != nil || !session.ExpiresAt.After(s.now().UTC()) {
		return Principal{}, ErrUnauthorized
	}
	account, err := s.store.AccountByID(ctx, session.AccountID)
	if err != nil || !account.Enabled {
		return Principal{}, ErrUnauthorized
	}
	return Principal{AccountID: account.ID, SessionID: session.ID, Username: account.Username}, nil
}

func (s *Service) ChangePassword(ctx context.Context, accountID, current, next string) (Account, error) {
	account, err := s.store.AccountByID(ctx, accountID)
	if err != nil || bcrypt.CompareHashAndPassword(account.PasswordHash, []byte(current)) != nil {
		return Account{}, ErrInvalidCredentials
	}
	if !validPassword(next) {
		return Account{}, ErrPasswordPolicy
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		return Account{}, err
	}
	now := s.now().UTC()
	return s.store.ChangePasswordAndRevokeSessions(ctx, account.ID, account.Revision, hash, now)
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	return s.store.RevokeSessionByTokenHash(ctx, hashToken(token), s.now().UTC())
}

func randomToken() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func hashToken(token string) []byte {
	hash := sha256.Sum256([]byte(token))
	return hash[:]
}

func validPassword(password string) bool {
	if len(password) < 10 {
		return false
	}
	var letter, digit bool
	for _, value := range password {
		letter = letter || unicode.IsLetter(value)
		digit = digit || unicode.IsDigit(value)
	}
	return letter && digit
}

type loginAttempt struct {
	count   int
	resetAt time.Time
}

type loginLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	items  map[string]loginAttempt
}

func newLoginLimiter(limit int, window time.Duration) *loginLimiter {
	return &loginLimiter{limit: limit, window: window, items: map[string]loginAttempt{}}
}

func (l *loginLimiter) blocked(key string, now time.Time) bool {
	if key == "ip:" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt, ok := l.items[key]
	if !ok || !now.Before(attempt.resetAt) {
		delete(l.items, key)
		return false
	}
	return attempt.count >= l.limit
}

// limiterSweepThreshold bounds the login-limiter map: every key is attacker
// controlled (username or client IP), so without a cap unauthenticated
// failures with random usernames grow the map without limit.
const limiterSweepThreshold = 10000

func (l *loginLimiter) failure(key string, now time.Time) {
	if key == "ip:" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.items) > limiterSweepThreshold {
		for item, attempt := range l.items {
			if !now.Before(attempt.resetAt) {
				delete(l.items, item)
			}
		}
	}
	attempt := l.items[key]
	if !now.Before(attempt.resetAt) {
		attempt = loginAttempt{resetAt: now.Add(l.window)}
	}
	attempt.count++
	l.items[key] = attempt
}

func (l *loginLimiter) success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.items, key)
}

func (l *loginLimiter) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = map[string]loginAttempt{}
}
