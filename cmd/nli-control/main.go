package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"nexora.local/nli-v20/internal/auth"
	"nexora.local/nli-v20/internal/controlplane"
	"nexora.local/nli-v20/internal/engine"
)

func loadConfig(path string) (controlplane.Config, error) {
	cfg := controlplane.Config{
		ListenAddress:             "127.0.0.1:12382",
		StateDir:                  "/var/lib/nli-controller",
		WebDir:                    "/usr/share/nli/webview_ver20",
		EnrollmentTokenEnv:        "NLI_ENROLLMENT_TOKEN",
		BootstrapAdminPasswordEnv: "NLI_BOOTSTRAP_ADMIN_PASSWORD",
		CommandLeaseSeconds:       120,
		EventRetention:            50000,
	}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("decode config %s: %w", path, err)
		}
		return cfg, nil
	}
	if !os.IsNotExist(err) {
		return cfg, err
	}
	fmt.Fprintf(os.Stderr, "nli-control: config file %s not found, using defaults\n", path)
	return cfg, nil
}

func resetAdminPassword(cfg controlplane.Config, input io.Reader) error {
	scanner := bufio.NewScanner(io.LimitReader(input, 4097))
	scanner.Buffer(make([]byte, 128), 4096)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return err
		}
		return fmt.Errorf("new admin password is required on stdin")
	}
	password := strings.TrimSuffix(scanner.Text(), "\r")
	if scanner.Scan() {
		return fmt.Errorf("stdin must contain exactly one password line")
	}
	store, err := auth.NewStoreWithBootstrap(filepath.Join(cfg.StateDir, "users.json"), "")
	if err != nil {
		return fmt.Errorf("load auth store: %w", err)
	}
	if err := store.ResetPassword("admin", password); err != nil {
		return err
	}
	return nil
}

func main() {
	fs := flag.NewFlagSet("nli-control", flag.ExitOnError)
	configFile := fs.String("config", "/etc/nli/controller.json", "control plane config file")
	versionFlag := fs.Bool("version", false, "print version")
	resetAdmin := fs.Bool("reset-admin-password-stdin", false, "reset the existing admin password from one stdin line while the service is stopped")
	fs.Parse(os.Args[1:])

	if *versionFlag {
		fmt.Println("nli-control", engine.Version)
		return
	}

	cfg, err := loadConfig(*configFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nli-control:", err)
		os.Exit(1)
	}
	if *resetAdmin {
		if err := resetAdminPassword(cfg, os.Stdin); err != nil {
			fmt.Fprintln(os.Stderr, "nli-control: reset admin password:", err)
			os.Exit(1)
		}
		fmt.Println("nli-control: admin password reset")
		return
	}

	if err := os.MkdirAll(cfg.StateDir, 0750); err != nil {
		fmt.Fprintln(os.Stderr, "nli-control: state dir:", err)
		os.Exit(1)
	}

	srv, err := controlplane.New(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nli-control:", err)
		os.Exit(1)
	}

	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, "nli-control:", err)
		os.Exit(1)
	}
}
