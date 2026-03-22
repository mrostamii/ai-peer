// Package main is the root command entrypoint for the ai-peer node and tooling.
// Binaries and subcommands will be expanded in later phases.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"github.com/mrostamii/ai-peer/pkg/backend/ollama"
	"github.com/mrostamii/ai-peer/pkg/config"
	"github.com/mrostamii/ai-peer/pkg/gateway"
	"github.com/mrostamii/ai-peer/pkg/node"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("ai-peer")
		fmt.Println("usage: ai-peer config-check -file ./node.yaml")
		return
	}

	switch os.Args[1] {
	case "config-check":
		runConfigCheck(os.Args[2:])
	case "node":
		runNode(os.Args[2:])
	case "gateway":
		runGateway(os.Args[2:])
	default:
		fmt.Printf("unknown command: %s\n", os.Args[1])
		os.Exit(2)
	}
}

func runConfigCheck(args []string) {
	fs := flag.NewFlagSet("config-check", flag.ExitOnError)
	file := fs.String("file", "./node.yaml", "path to node.yaml")
	_ = fs.Parse(args)

	cfg, err := config.Load(*file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config invalid: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("config valid")
	fmt.Printf("node=%s tcp=%d quic=%d backend=%s models=%d\n",
		cfg.Node.Name,
		cfg.Listen.TCPPort,
		cfg.Listen.QUICPort,
		cfg.Backend.BaseURL,
		len(cfg.Models.Advertised),
	)
}

func runNode(args []string) {
	if len(args) == 0 {
		fmt.Println("usage: ai-peer node <start|status> -file ./node.yaml")
		os.Exit(2)
	}
	switch args[0] {
	case "start":
		runNodeStart(args[1:])
	case "status":
		runNodeStatus(args[1:])
	default:
		fmt.Printf("unknown node command: %s\n", args[0])
		os.Exit(2)
	}
}

func runNodeStart(args []string) {
	fs := flag.NewFlagSet("node start", flag.ExitOnError)
	file := fs.String("file", "./node.yaml", "path to node.yaml")
	_ = fs.Parse(args)

	cfg, err := config.Load(*file)
	if err != nil {
		log.Fatalf("config invalid: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cli := ollama.New(cfg.Backend.BaseURL)
	if err := cli.HealthCheck(ctx); err != nil {
		log.Fatalf("backend health check failed: %v", err)
	}

	available, err := cli.ListModels(ctx)
	if err != nil {
		log.Fatalf("list backend models failed: %v", err)
	}

	log.Printf("node start: name=%s tcp=%d quic=%d backend=%s", cfg.Node.Name, cfg.Listen.TCPPort, cfg.Listen.QUICPort, cfg.Backend.BaseURL)
	log.Printf("backend models: %v", available)
	for _, want := range cfg.Models.Advertised {
		if !slices.Contains(available, want) {
			log.Printf("warning: advertised model %q not found in backend /api/tags", want)
		}
	}
	rt, err := node.Start(ctx, cfg)
	if err != nil {
		log.Fatalf("node runtime failed to start: %v", err)
	}
	defer func() {
		if err := rt.Close(); err != nil {
			log.Printf("node shutdown warning: %v", err)
		}
	}()

	log.Printf("node is ready (libp2p + dht + backend); press Ctrl+C to stop")

	<-ctx.Done()
	log.Printf("shutdown signal received; exiting")
}

func runNodeStatus(args []string) {
	fs := flag.NewFlagSet("node status", flag.ExitOnError)
	file := fs.String("file", "./node.yaml", "path to node.yaml")
	_ = fs.Parse(args)

	cfg, err := config.Load(*file)
	if err != nil {
		log.Fatalf("config invalid: %v", err)
	}

	cli := ollama.New(cfg.Backend.BaseURL)
	err = cli.HealthCheck(context.Background())
	health := "ok"
	if err != nil {
		health = "error: " + err.Error()
	}

	fmt.Printf("node=%s\n", cfg.Node.Name)
	fmt.Printf("backend=%s\n", cfg.Backend.BaseURL)
	fmt.Printf("health=%s\n", health)
	fmt.Printf("advertised_models=%v\n", cfg.Models.Advertised)
}

func runGateway(args []string) {
	if len(args) == 0 {
		fmt.Println("usage: ai-peer gateway start [-file ./node.yaml] [-listen 127.0.0.1:8080] [-ollama http://127.0.0.1:11434]")
		os.Exit(2)
	}
	switch args[0] {
	case "start":
		runGatewayStart(args[1:])
	default:
		fmt.Printf("unknown gateway command: %s\n", args[0])
		os.Exit(2)
	}
}

func runGatewayStart(args []string) {
	fs := flag.NewFlagSet("gateway start", flag.ExitOnError)
	file := fs.String("file", "./node.yaml", "path to node.yaml")
	listen := fs.String("listen", "", "gateway listen address override")
	ollamaBase := fs.String("ollama", "", "ollama base URL override (optional)")
	_ = fs.Parse(args)

	cfg, err := config.Load(*file)
	if err != nil {
		log.Fatalf("config invalid: %v", err)
	}

	resolvedListen := cfg.Gateway.Listen
	if *listen != "" {
		resolvedListen = *listen
	}
	resolvedOllama := cfg.Backend.BaseURL
	if *ollamaBase != "" {
		resolvedOllama = *ollamaBase
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("gateway start: listen=%s ollama=%s", resolvedListen, resolvedOllama)
	proxy := gateway.NewOpenAIProxy(resolvedListen, resolvedOllama)
	if err := proxy.Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("gateway failed: %v", err)
	}
	log.Printf("gateway stopped")
}
