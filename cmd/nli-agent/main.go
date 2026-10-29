package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"nexora.local/nli-v20/internal/agent"
	"nexora.local/nli-v20/internal/engine"
)

func main() {
	config := flag.String("config", "/etc/nli/nli.json", "JSON configuration file")
	once := flag.Bool("once", false, "read current files to EOF then exit")
	version := flag.Bool("version", false, "print version")
	flag.Parse()

	if *version {
		fmt.Println("nli-agent", engine.Version)
		return
	}
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments")
		os.Exit(2)
	}

	c, err := agent.Load(*config)
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration:", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err = agent.Run(ctx, c, *once); err != nil {
		fmt.Fprintln(os.Stderr, "agent:", err)
		os.Exit(1)
	}
}
