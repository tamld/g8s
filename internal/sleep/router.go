package sleep

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// EventRouter dispatches notifications to configured destinations per DEBT-50.
type EventRouter interface {
	Route(ctx context.Context, event Event, isSleeping bool) error
}

// StderrRouter writes alerts to standard error or a designated io.Writer.
type StderrRouter struct {
	mu     sync.Mutex
	writer io.Writer
}

// NewStderrRouter constructs a StderrRouter.
func NewStderrRouter(w io.Writer) *StderrRouter {
	if w == nil {
		w = os.Stderr
	}
	return &StderrRouter{writer: w}
}

// Route outputs event messages according to sleep policy.
func (r *StderrRouter) Route(_ context.Context, event Event, isSleeping bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// If operator is sleeping, only immediate route critical events
	if isSleeping && event.Severity != SeverityCritical {
		return nil
	}

	prefix := "[EVENT]"
	if event.Severity == SeverityCritical {
		prefix = "🚨 [CRITICAL ALERT]"
	} else if event.Severity == SeverityWarning {
		prefix = "⚠️ [WARNING]"
	}

	_, err := fmt.Fprintf(r.writer, "%s %s: %s (session=%s)\n",
		prefix, event.Type, event.Message, event.SessionID)
	return err
}

// FileRouter appends event records to a notification sink file.
type FileRouter struct {
	mu       sync.Mutex
	filePath string
}

// NewFileRouter constructs a FileRouter writing to path.
func NewFileRouter(path string) *FileRouter {
	return &FileRouter{filePath: path}
}

// Route appends event JSON to destination file if policy allows.
func (r *FileRouter) Route(_ context.Context, event Event, isSleeping bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if isSleeping && event.Severity != SeverityCritical {
		return nil
	}

	f, err := os.OpenFile(r.filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("file router: open %s: %w", r.filePath, err)
	}
	defer f.Close()

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("file router: marshal: %w", err)
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

// TelegramRouter dispatches urgent messages to Telegram Bot API.
type TelegramRouter struct {
	mu         sync.Mutex
	BotToken   string
	ChatID     string
	HTTPClient *http.Client
	BaseURL    string // Customizable for testing/mocking
}

// NewTelegramRouter constructs a TelegramRouter with bot credentials.
func NewTelegramRouter(botToken, chatID string) *TelegramRouter {
	return &TelegramRouter{
		BotToken:   botToken,
		ChatID:     chatID,
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		BaseURL:    "https://api.telegram.org",
	}
}

// Route sends critical telegram message when configured.
func (r *TelegramRouter) Route(ctx context.Context, event Event, isSleeping bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if isSleeping && event.Severity != SeverityCritical {
		return nil
	}
	if r.BotToken == "" || r.ChatID == "" {
		// Unconfigured Telegram router is a no-op
		return nil
	}

	text := fmt.Sprintf("🚨 *g8s Alert: %s*\n%s\nSession: `%s`", event.Type, event.Message, event.SessionID)
	reqBody, err := json.Marshal(map[string]string{
		"chat_id":    r.ChatID,
		"text":       text,
		"parse_mode": "Markdown",
	})
	if err != nil {
		return fmt.Errorf("telegram: marshal: %w", err)
	}

	apiURL := fmt.Sprintf("%s/bot%s/sendMessage", r.BaseURL, r.BotToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("telegram: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram: bad response (%d): %s", resp.StatusCode, string(body))
	}
	return nil
}

// CompositeRouter distributes events across multiple routers.
type CompositeRouter struct {
	routers []EventRouter
}

// NewCompositeRouter wraps multiple routers.
func NewCompositeRouter(routers ...EventRouter) *CompositeRouter {
	return &CompositeRouter{routers: routers}
}

// Route distributes event across all child routers.
func (c *CompositeRouter) Route(ctx context.Context, event Event, isSleeping bool) error {
	for _, r := range c.routers {
		if r != nil {
			if err := r.Route(ctx, event, isSleeping); err != nil {
				// Continue routing to others even if one router fails
				continue
			}
		}
	}
	return nil
}
