package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/monitor"
)

func init() {
	logDir := filepath.Join(monitor.ConfigDir(), "log")
	os.MkdirAll(logDir, 0755)
	logFile, err := os.OpenFile(
		filepath.Join(logDir, "channel.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0644,
	)
	if err != nil {
		log.SetOutput(os.Stderr)
	} else {
		log.SetOutput(io.MultiWriter(os.Stderr, logFile))
	}
	log.SetFlags(log.Ldate | log.Ltime)
	log.SetPrefix("endless-channel: ")
}

// channelNotification is the payload sent via MCP channel notification.
type channelNotification struct {
	Content string            `json:"content"`
	Meta    map[string]string `json:"meta,omitempty"`
}

// notifyRequest is the HTTP POST body from the Endless CLI.
type notifyRequest struct {
	Event     string `json:"event"`
	ChannelID string `json:"channel_id"`
	Preview   string `json:"preview"`
}

// processID returns an identifier for the current Claude Code session's process.
// Uses TMUX_PANE when in tmux, falls back to parent PID.
func processID() string {
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		return pane
	}
	return fmt.Sprintf("pid:%d", os.Getppid())
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	procID := processID()
	pid := os.Getpid()

	// Pick a free port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	// Register port immediately — no dependency on ai_sessions
	if err := monitor.RegisterChannelPort(procID, port, pid); err != nil {
		log.Printf("[endless-channel] failed to register port: %v", err)
	} else {
		log.Printf("[endless-channel] registered: process=%s port=%d pid=%d", procID, port, pid)
	}

	// Create the MCP server with claude/channel capability
	server := mcp.NewServer(
		&mcp.Implementation{
			Name:    "endless-channel",
			Version: "0.1.0",
		},
		&mcp.ServerOptions{
			Instructions: `Events from Endless arrive as <channel source="endless-channel" event_type="..." channel_id="...">.
When you see event_type="message", run: endless channel inbox
When you see event_type="connected", a session has connected to your channel.
Do not call endless channel inbox unless prompted by a channel event or the user.`,
			Capabilities: &mcp.ServerCapabilities{
				Experimental: map[string]any{
					"claude/channel": map[string]any{},
				},
			},
		},
	)

	// Connect to Claude Code over stdio
	session, err := server.Connect(ctx, &mcp.StdioTransport{}, nil)
	if err != nil {
		log.Fatalf("failed to connect: %v", err)
	}

	// Start HTTP server for incoming notifications
	mux := http.NewServeMux()
	mux.HandleFunc("POST /notify", func(w http.ResponseWriter, r *http.Request) {
		var req notifyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		notification := channelNotification{
			Content: req.Preview,
			Meta: map[string]string{
				"event_type": req.Event,
				"channel_id": req.ChannelID,
			},
		}

		if err := session.SendNotification(ctx, "notifications/claude/channel", notification); err != nil {
			http.Error(w, fmt.Sprintf("send failed: %v", err), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})

	httpServer := &http.Server{Handler: mux}
	go func() {
		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("http server error: %v", err)
		}
	}()

	// Wait for shutdown signal or session end
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	sessionDone := make(chan error, 1)
	go func() {
		sessionDone <- session.Wait()
	}()

	select {
	case <-sigCh:
	case <-sessionDone:
	}

	// Cleanup — errors intentionally ignored during shutdown; best-effort cleanup only
	_ = monitor.UnregisterChannelPort(procID)

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutCancel()
	_ = httpServer.Shutdown(shutCtx) // best-effort graceful shutdown
}
