package domain

import (
	"fmt"
	"testing"
)

func TestListUsersNumericOrder(t *testing.T) {
	t.Run("sorts more than 10 users numerically by ID", func(t *testing.T) {
		svc := NewUserService()
		for i := 2; i <= 12; i++ {
			name := fmt.Sprintf("User %d", i)
			email := fmt.Sprintf("user%d@example.com", i)
			if _, err := svc.CreateUser(t.Context(), name, email, "developer"); err != nil {
				t.Fatalf("CreateUser(%d) failed: %v", i, err)
			}
		}

		users, err := svc.ListUsers(t.Context())
		if err != nil {
			t.Fatalf("ListUsers failed: %v", err)
		}
		if len(users) != 12 {
			t.Fatalf("expected 12 users, got %d", len(users))
		}

		for i, u := range users {
			wantID := fmt.Sprintf("u-%d", i+1)
			if u.ID != wantID {
				t.Fatalf("users[%d].ID = %q, want %q", i, u.ID, wantID)
			}
		}
	})

	t.Run("tie-breaks equal numeric part by ID", func(t *testing.T) {
		svc := NewUserService()
		svc.users["u-2"] = User{ID: "u-2", Name: "Two"}
		svc.users["u-02"] = User{ID: "u-02", Name: "ZeroTwo"}

		users, err := svc.ListUsers(t.Context())
		if err != nil {
			t.Fatalf("ListUsers failed: %v", err)
		}
		want := []string{"u-1", "u-02", "u-2"}
		if len(users) != len(want) {
			t.Fatalf("expected %d users, got %d", len(want), len(users))
		}
		for i, id := range want {
			if users[i].ID != id {
				t.Fatalf("users[%d].ID = %q, want %q", i, users[i].ID, id)
			}
		}
	})
}
