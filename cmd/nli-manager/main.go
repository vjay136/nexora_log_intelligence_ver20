package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"nexora.local/nli-v20/internal/engine"
	"nexora.local/nli-v20/internal/manager"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	command := os.Args[1]
	switch command {
	case "serve":
		serveCmd(os.Args[2:])
	case "clients":
		clientsCmd(os.Args[2:])
	case "version", "--version":
		fmt.Println("nli-manager", engine.Version)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Println(`nli-manager ` + engine.Version + `
Commands:
  serve  [--config FILE]           start the manager server
  clients list [--endpoint URL]    list registered clients
  clients status HOST              show client detail
  version`)
}

func serveCmd(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configFile := fs.String("config", "/etc/nli/manager.json", "manager config file")
	fs.Parse(args)

	var cfg manager.Config
	if data, err := os.ReadFile(*configFile); err == nil {
		json.Unmarshal(data, &cfg)
	}

	s := manager.New(cfg)
	if err := s.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, "manager:", err)
		os.Exit(1)
	}
}

func clientsCmd(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: nli-manager clients <list|status HOST>")
		os.Exit(2)
	}
	endpoint := "http://127.0.0.1:12381"
	subCmd := args[0]
	switch subCmd {
	case "list":
		resp, err := apiGet(endpoint + "/api/v1/clients")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(resp)
	case "status":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: nli-manager clients status HOST")
			os.Exit(2)
		}
		resp, err := apiGet(endpoint + "/api/v1/clients/" + args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(resp)
	default:
		fmt.Fprintln(os.Stderr, "unknown subcommand:", subCmd)
		os.Exit(2)
	}
}

func apiGet(url string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return "", err
	}
	if len(body) > 1<<20 {
		return "", fmt.Errorf("manager response exceeds 1048576 bytes")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("manager returned %s: %s", resp.Status, string(body))
	}
	return string(body), nil
}
