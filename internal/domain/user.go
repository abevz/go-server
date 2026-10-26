package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound     = errors.New("resource not found")
	ErrInvalidInput = errors.New("invalid input")
	ErrConflict     = errors.New("resource already exists")
)

type User struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type UserService struct {
	mu      sync.RWMutex
	users   map[string]User
	counter int
}

func NewUserService() *UserService {
	s := &UserService{
		users:   make(map[string]User),
		counter: 1,
	}
	s.users["u-1"] = User{
		ID:        "u-1",
		Name:      "Aleksey",
		Email:     "aleksey@example.com",
		Role:      "platform-engineer",
		CreatedAt: time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC),
	}
	return s
}

func (s *UserService) GetUser(ctx context.Context, id string) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	if strings.TrimSpace(id) == "" {
		return User{}, fmt.Errorf("%w: id cannot be empty", ErrInvalidInput)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	u, ok := s.users[id]
	if !ok {
		return User{}, fmt.Errorf("%w: user %q", ErrNotFound, id)
	}
	return u, nil
}

func (s *UserService) CreateUser(ctx context.Context, name, email, role string) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
	role = strings.TrimSpace(role)

	if len(name) < 2 {
		return User{}, fmt.Errorf("%w: name must be at least 2 chars", ErrInvalidInput)
	}
	if !strings.Contains(email, "@") {
		return User{}, fmt.Errorf("%w: email must contain '@'", ErrInvalidInput)
	}
	if role == "" {
		role = "developer"
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, existing := range s.users {
		if strings.EqualFold(existing.Email, email) {
			return User{}, fmt.Errorf("%w: email %q is already taken", ErrConflict, email)
		}
	}

	s.counter++
	id := fmt.Sprintf("u-%d", s.counter)
	u := User{
		ID:        id,
		Name:      name,
		Email:     email,
		Role:      role,
		CreatedAt: time.Now().UTC().Truncate(time.Second),
	}
	s.users[id] = u
	return u, nil
}

func (s *UserService) ListUsers(ctx context.Context) ([]User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]User, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, u)
	}
	return out, nil
}
