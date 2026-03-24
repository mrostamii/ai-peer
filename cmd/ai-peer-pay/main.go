package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/mrostamii/ai-peer/pkg/x402client"
	"github.com/mrostamii/ai-peer/pkg/x402spike"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "chat":
		runChat(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Println("ai-peer-pay usage:")
	fmt.Println("  ai-peer-pay chat -url http://127.0.0.1:8080/v1/chat/completions -model qwen2.5:3b -message \"say hi\"")
	fmt.Println("env:")
	fmt.Println("  EVM_PRIVATE_KEY=0x... (required unless -private-key is set)")
}

func runChat(args []string) {
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	url := fs.String("url", "http://127.0.0.1:8080/v1/chat/completions", "chat completions endpoint URL")
	model := fs.String("model", "qwen2.5:3b", "model name")
	message := fs.String("message", "say hi", "user message content")
	stream := fs.Bool("stream", true, "request streaming response")
	privateKey := fs.String("private-key", "", "optional private key override")
	_ = fs.Parse(args)

	client, err := x402client.NewFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "wallet error: %v\n", err)
		os.Exit(2)
	}
	if strings.TrimSpace(*privateKey) != "" {
		client.PrivateKey = strings.TrimSpace(*privateKey)
	}

	body := map[string]any{
		"model":  *model,
		"stream": *stream,
		"messages": []map[string]string{
			{"role": "user", "content": *message},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "request marshal error: %v\n", err)
		os.Exit(1)
	}

	req, err := http.NewRequest(http.MethodPost, *url, bytes.NewReader(raw))
	if err != nil {
		fmt.Fprintf(os.Stderr, "request create error: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.DoWithPayment(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "request failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if settleHeader := resp.Header.Get("PAYMENT-RESPONSE"); strings.TrimSpace(settleHeader) != "" {
		var settle x402spike.SettlementResponse
		if err := x402spike.DecodeBase64JSON(settleHeader, &settle); err == nil {
			settleRaw, _ := json.MarshalIndent(settle, "", "  ")
			fmt.Fprintf(os.Stderr, "payment settlement: %s\n", settleRaw)
		}
	}
	if reqID := strings.TrimSpace(resp.Header.Get("X-AI-Peer-Request-ID")); reqID != "" {
		fmt.Fprintf(os.Stderr, "request_id=%s\n", reqID)
	}

	if *stream {
		if resp.StatusCode >= 400 {
			respBody, _ := io.ReadAll(resp.Body)
			fmt.Fprintf(os.Stderr, "status=%d body=%s\n", resp.StatusCode, string(respBody))
			os.Exit(1)
		}
		if _, err := io.Copy(os.Stdout, resp.Body); err != nil {
			fmt.Fprintf(os.Stderr, "stream copy error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read response error: %v\n", err)
		os.Exit(1)
	}
	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "status=%d body=%s\n", resp.StatusCode, string(respBody))
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(respBody)
}
