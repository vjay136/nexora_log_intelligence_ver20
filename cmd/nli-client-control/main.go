package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"nexora.local/nli-v20/internal/controlclient"
	"nexora.local/nli-v20/internal/engine"
)

func main() {
	configPath := flag.String("config", "/etc/nli/client-control.json", "client control configuration")
	version := flag.Bool("version", false, "print version")
	once := flag.Bool("once", false, "run one heartbeat/command cycle")
	flag.Parse()
	if *version {
		fmt.Println("nli-client-control", engine.Version)
		return
	}
	cfg, err := controlclient.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nli-client-control:", err)
		os.Exit(1)
	}
	client, err := controlclient.New(cfg, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nli-client-control:", err)
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if *once {
		err = client.RunOnce(ctx)
	} else {
		err = client.Run(ctx)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "nli-client-control:", err)
		os.Exit(1)
	}
}
