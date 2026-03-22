package auth

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

// ErrInvalidCredentials is returned when username or password is wrong.
var ErrInvalidCredentials = errors.New("auth: invalid credentials")

// User is a minimal identity record.
type User struct {
	ID           string
	Username     string
	passwordHash string
}

// UserStore is an in-memory credential registry.
// Replace with a database-backed implementation in production.
type UserStore struct {
	users map[string]*User // keyed by username
}

// NewUserStore creates a UserStore from a username → plaintext-password map.
// Passwords are bcrypt-hashed at construction time (bcrypt.DefaultCost = 10).
func NewUserStore(credentials map[string]string) (*UserStore, error) {
	store := &UserStore{users: make(map[string]*User, len(credentials))}
	for username, password := range credentials {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		store.users[username] = &User{
			ID:           username, // use username as ID; replace with UUID in production
			Username:     username,
			passwordHash: string(hash),
		}
	}
	return store, nil
}

// Authenticate validates credentials. Both branches take the same wall-clock
// time (constant-time comparison) to prevent username-enumeration via timing.
func (s *UserStore) Authenticate(username, password string) (*User, error) {
	u, ok := s.users[username]
	if !ok {
		// Perform a dummy bcrypt compare so the response time is indistinguishable
		// from the "wrong password" path, preventing username enumeration.
		_ = bcrypt.CompareHashAndPassword(
			[]byte("$2a$10$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			[]byte(password),
		)
		return nil, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.passwordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}
	return u, nil
}
