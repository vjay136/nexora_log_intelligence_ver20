package auth_test

import (
	"os"
	"testing"

	"nexora.local/nli-v20/internal/auth"
)

func tmpPath(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("", "nli-auth-*.json")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	os.Remove(f.Name())
	t.Cleanup(func() { os.Remove(f.Name()) })
	return f.Name()
}

func TestDefaultAdminCreated(t *testing.T) {
	s, err := auth.NewStoreWithBootstrap(tmpPath(t), "Test-admin-password-123!")
	if err != nil {
		t.Fatal(err)
	}
	users := s.ListUsers()
	if len(users) != 1 || users[0].Username != "admin" {
		t.Errorf("expected default admin user, got %d users", len(users))
	}
}

func TestAuthenticate(t *testing.T) {
	s, _ := auth.NewStoreWithBootstrap(tmpPath(t), "Test-admin-password-123!")
	token, user, err := s.Authenticate("admin", "Test-admin-password-123!")
	if err != nil {
		t.Fatalf("auth failed: %v", err)
	}
	if token == "" {
		t.Error("expected non-empty token")
	}
	if user.Role != auth.RoleAdmin {
		t.Errorf("role = %q, want admin", user.Role)
	}
}

func TestWrongPassword(t *testing.T) {
	s, _ := auth.NewStoreWithBootstrap(tmpPath(t), "Test-admin-password-123!")
	_, _, err := s.Authenticate("admin", "wrongpassword")
	if err == nil {
		t.Error("expected error for wrong password")
	}
}

func TestValidateToken(t *testing.T) {
	s, _ := auth.NewStoreWithBootstrap(tmpPath(t), "Test-admin-password-123!")
	token, _, _ := s.Authenticate("admin", "Test-admin-password-123!")
	username, role, err := s.ValidateToken(token)
	if err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if username != "admin" || role != auth.RoleAdmin {
		t.Errorf("got username=%q role=%q", username, role)
	}
}

func TestAdminOnlyCanCreateUsers(t *testing.T) {
	s, _ := auth.NewStoreWithBootstrap(tmpPath(t), "Test-admin-password-123!")

	// viewer cannot create
	_, err := s.CreateUser(auth.RoleViewer, "newuser", "new@test.com", auth.RoleViewer, "Test-user-password-123!")
	if err == nil {
		t.Error("expected error: viewer cannot create users")
	}

	// admin can create
	u, err := s.CreateUser(auth.RoleAdmin, "alice", "alice@test.com", auth.RoleOps, "Test-user-password-123!")
	if err != nil {
		t.Fatalf("admin create failed: %v", err)
	}
	if u.Username != "alice" || u.Role != auth.RoleOps {
		t.Errorf("created user mismatch: %+v", u)
	}
}

func TestDeactivateUser(t *testing.T) {
	s, _ := auth.NewStoreWithBootstrap(tmpPath(t), "Test-admin-password-123!")
	s.CreateUser(auth.RoleAdmin, "bob", "bob@test.com", auth.RoleViewer, "Test-bob-password-123!")
	if err := s.DeactivateUser(auth.RoleAdmin, "bob"); err != nil {
		t.Fatal(err)
	}
	// Deactivated user cannot log in
	_, _, err := s.Authenticate("bob", "Test-bob-password-123!")
	if err == nil {
		t.Error("deactivated user should not authenticate")
	}
}

func TestInvalidToken(t *testing.T) {
	s, _ := auth.NewStoreWithBootstrap(tmpPath(t), "Test-admin-password-123!")
	_, _, err := s.ValidateToken("invalid.token.here")
	if err == nil {
		t.Error("expected error for invalid token")
	}
}

func TestResetPasswordPreservesUsersAndReactivatesAccount(t *testing.T) {
	s, _ := auth.NewStoreWithBootstrap(tmpPath(t), "Test-admin-password-123!")
	if _, err := s.CreateUser(auth.RoleAdmin, "operator", "operator@test.com", auth.RoleOps, "Test-operator-password-123!"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeactivateUser(auth.RoleAdmin, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetPassword("admin", "Replacement-admin-password-2026!"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Authenticate("admin", "Test-admin-password-123!"); err == nil {
		t.Fatal("old password still authenticates after reset")
	}
	if _, user, err := s.Authenticate("admin", "Replacement-admin-password-2026!"); err != nil || !user.Active {
		t.Fatalf("reset admin did not authenticate as active: user=%+v err=%v", user, err)
	}
	if users := s.ListUsers(); len(users) != 2 {
		t.Fatalf("reset discarded existing users: got %d", len(users))
	}
	if err := s.ResetPassword("admin", "too-short"); err == nil {
		t.Fatal("short recovery password was accepted")
	}
}
