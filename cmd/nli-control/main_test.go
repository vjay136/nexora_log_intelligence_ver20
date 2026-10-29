package main

import (
	"path/filepath"
	"strings"
	"testing"

	"nexora.local/nli-v20/internal/auth"
	"nexora.local/nli-v20/internal/controlplane"
)

func TestResetAdminPasswordFromStdin(t *testing.T) {
	state := t.TempDir()
	store, err := auth.NewStoreWithBootstrap(filepath.Join(state, "users.json"), "Original-admin-password-2026!")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateUser(auth.RoleAdmin, "viewer", "viewer@example.test", auth.RoleViewer, "Viewer-password-2026!"); err != nil {
		t.Fatal(err)
	}

	cfg := controlplane.Config{StateDir: state}
	if err := resetAdminPassword(cfg, strings.NewReader("Replacement-admin-password-2026!\n")); err != nil {
		t.Fatal(err)
	}

	reloaded, err := auth.NewStoreWithBootstrap(filepath.Join(state, "users.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := reloaded.Authenticate("admin", "Original-admin-password-2026!"); err == nil {
		t.Fatal("original admin password still authenticates")
	}
	if _, _, err := reloaded.Authenticate("admin", "Replacement-admin-password-2026!"); err != nil {
		t.Fatalf("replacement password does not authenticate: %v", err)
	}
	if users := reloaded.ListUsers(); len(users) != 2 {
		t.Fatalf("offline reset discarded users: %d", len(users))
	}
}

func TestResetAdminPasswordRejectsMultipleLines(t *testing.T) {
	state := t.TempDir()
	if _, err := auth.NewStoreWithBootstrap(filepath.Join(state, "users.json"), "Original-admin-password-2026!"); err != nil {
		t.Fatal(err)
	}
	if err := resetAdminPassword(controlplane.Config{StateDir: state}, strings.NewReader("Replacement-admin-password-2026!\nunexpected\n")); err == nil {
		t.Fatal("multiple password lines were accepted")
	}
}
