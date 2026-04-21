// Command tester is a small web UI + signing proxy that lets a human play the
// role of a Guesty guest against a running inquiryiq service. It serves three
// jobs in one binary so the browser stays untrusted:
//
//  1. Serve the arrow-js chat UI under /.
//  2. Sign and forward webhook payloads to the real service endpoint.
//  3. Intercept the outbound Guesty send-message call so the UI can show
//     what the bot replied; every other Guesty path is transparently
//     forwarded to the underlying Mockoon so the service still sees real
//     listing/availability data.
//
// The binary is designed to run inside the mocks compose alongside the
// service and Mockoon; it holds the same GUESTY_WEBHOOK_SECRET the service
// verifies against and is not a production artifact.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := loadConfig()
	srv := newServer(cfg, log)

	httpServer := &http.Server{
		Addr:              cfg.listen,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("tester_listen",
			slog.String("addr", cfg.listen),
			slog.String("service", cfg.serviceURL),
			slog.String("mockoon", cfg.mockoonURL),
		)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen_failed", slog.String("err", err.Error()))
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
}

type config struct {
	listen        string
	serviceURL    string
	mockoonURL    string
	webhookSecret string
	staticDir     string
}

func loadConfig() config {
	return config{
		listen:        envOr("TESTER_LISTEN", ":4000"),
		serviceURL:    envOr("INQUIRYIQ_URL", "http://localhost:8080"),
		mockoonURL:    envOr("MOCKOON_URL", "http://localhost:3001"),
		webhookSecret: envOr("GUESTY_WEBHOOK_SECRET", "whsec_demo"),
		staticDir:     envOr("TESTER_STATIC", "./static"),
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// conversationLog is the in-memory turn-by-turn log the UI polls. Each entry
// is a single post (guest or bot) in send order. Bot posts are captured when
// the service calls the intercepted send-message endpoint; guest posts are
// captured when the UI sends a message through /api/send. Reset-on-restart
// is acceptable because the tester is a manual tool, not a durable record.
type conversationLog struct {
	mu    sync.RWMutex
	items map[string][]logEntry // keyed by conversation_id
}

type logEntry struct {
	Role string    `json:"role"` // "guest" | "bot" | "note"
	Body string    `json:"body"`
	At   time.Time `json:"at"`
}

func (c *conversationLog) append(convID string, e logEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[convID] = append(c.items[convID], e)
}

func (c *conversationLog) list(convID string) []logEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]logEntry, len(c.items[convID]))
	copy(out, c.items[convID])
	return out
}

type server struct {
	cfg        config
	log        *slog.Logger
	logs       *conversationLog
	mockoonRev *httputil.ReverseProxy
}

func newServer(cfg config, log *slog.Logger) *server {
	target, err := url.Parse(cfg.mockoonURL)
	if err != nil {
		panic(fmt.Errorf("bad MOCKOON_URL %q: %w", cfg.mockoonURL, err))
	}
	return &server{
		cfg:        cfg,
		log:        log,
		logs:       &conversationLog{items: map[string][]logEntry{}},
		mockoonRev: httputil.NewSingleHostReverseProxy(target),
	}
}

func (s *server) routes() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("POST /api/send", s.handleSend)
	api.HandleFunc("GET /api/escalations", s.handleEscalations)
	api.HandleFunc("GET /api/conversations/{id}", s.handleConversation)
	api.HandleFunc("GET /api/health", s.handleHealth)
	api.HandleFunc("POST /communication/conversations/{id}/send-message", s.handleSendMessage)

	static := http.FileServer(http.Dir(s.cfg.staticDir))

	// Single dispatcher: API + intercept routes first (most specific), then
	// the Mockoon proxy for every Guesty-shaped path, then the static file
	// server as the fallthrough for the UI itself.
	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/"),
			strings.HasPrefix(r.URL.Path, "/communication/"):
			api.ServeHTTP(w, r)
		case isGuestyPath(r.URL.Path):
			s.mockoonRev.ServeHTTP(w, r)
		default:
			static.ServeHTTP(w, r)
		}
	})
	return requestLogger(s.log)(root)
}

// isGuestyPath flags the URL prefixes the service uses to talk to Guesty.
// Keeping the list explicit (rather than "anything non-UI") means the UI
// can add its own /favicon.ico or /health without those leaking to Mockoon.
func isGuestyPath(p string) bool {
	prefixes := []string{"/listings/", "/availability-pricing/", "/communication/"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			next.ServeHTTP(w, r)
			log.Info("http",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Duration("dur", time.Since(started)),
			)
		})
	}
}

// sendRequest is the JSON body POST /api/send accepts. The UI supplies the
// minimum set of fields a single guest turn needs; the server fills the rest
// (ids, timestamps, reservation stub) before signing and forwarding.
type sendRequest struct {
	ConversationID string `json:"conversation_id"`
	ReservationID  string `json:"reservation_id"`
	GuestName      string `json:"guest_name"`
	GuestID        string `json:"guest_id"`
	Platform       string `json:"platform"`
	Body           string `json:"body"`
}

func (s *server) handleSend(w http.ResponseWriter, r *http.Request) {
	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	req = req.applyDefaults()
	if strings.TrimSpace(req.Body) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body required"})
		return
	}

	envelope := buildWebhookEnvelope(req)
	payload, err := json.Marshal(envelope)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	svixID := "msg_" + uuid.NewString()
	svixTS := fmt.Sprintf("%d", time.Now().Unix())
	signature := signSvix(s.cfg.webhookSecret, svixID, svixTS, payload)

	fwd, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		s.cfg.serviceURL+"/webhooks/guesty/message-received",
		bytes.NewReader(payload))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	fwd.Header.Set("Content-Type", "application/json")
	fwd.Header.Set("svix-id", svixID)
	fwd.Header.Set("svix-timestamp", svixTS)
	fwd.Header.Set("svix-signature", "v1,"+signature)
	fwd.Header.Set("user-agent", "Svix-Webhooks/tester")

	resp, err := http.DefaultClient.Do(fwd)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "forward failed: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// Record the guest post in the UI-visible log so the chat shows it
	// immediately, before the service's async pipeline produces the bot reply.
	s.logs.append(req.ConversationID, logEntry{
		Role: "guest",
		Body: req.Body,
		At:   envelope.Message.CreatedAt,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"conversation_id": req.ConversationID,
		"service_status":  resp.StatusCode,
		"service_body":    string(body),
	})
}

func (r sendRequest) applyDefaults() sendRequest {
	if r.ConversationID == "" {
		r.ConversationID = "conv_tester_default"
	}
	if r.ReservationID == "" {
		r.ReservationID = "res_tester_default"
	}
	if r.GuestName == "" {
		r.GuestName = "Tester"
	}
	if r.GuestID == "" {
		r.GuestID = "guest_tester"
	}
	if r.Platform == "" {
		r.Platform = "airbnb2"
	}
	return r
}

type webhookEnvelope struct {
	Event         string            `json:"event"`
	ReservationID string            `json:"reservationId"`
	Message       webhookMessage    `json:"message"`
	Conversation  webhookConv       `json:"conversation"`
	Meta          map[string]string `json:"meta"`
}

type webhookMessage struct {
	PostID    string    `json:"postId"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	Type      string    `json:"type"`
	Module    string    `json:"module"`
}

type webhookConv struct {
	ID          string              `json:"_id"`
	GuestID     string              `json:"guestId"`
	Language    string              `json:"language"`
	Status      string              `json:"status"`
	Integration map[string]string   `json:"integration"`
	Meta        map[string]any      `json:"meta"`
	Thread      []map[string]string `json:"thread"`
}

func buildWebhookEnvelope(req sendRequest) webhookEnvelope {
	now := time.Now().UTC()
	return webhookEnvelope{
		Event:         "reservation.messageReceived",
		ReservationID: req.ReservationID,
		Message: webhookMessage{
			PostID:    "msg_" + uuid.NewString(),
			Body:      req.Body,
			CreatedAt: now,
			Type:      "fromGuest",
			Module:    req.Platform,
		},
		Conversation: webhookConv{
			ID:          req.ConversationID,
			GuestID:     req.GuestID,
			Language:    "en",
			Status:      "OPEN",
			Integration: map[string]string{"platform": req.Platform},
			Meta: map[string]any{
				"guestName": req.GuestName,
				"reservations": []map[string]string{{
					"_id":              req.ReservationID,
					"checkIn":          now.Add(48 * time.Hour).Format(time.RFC3339),
					"checkOut":         now.Add(96 * time.Hour).Format(time.RFC3339),
					"confirmationCode": "TESTER1",
				}},
			},
			Thread: []map[string]string{},
		},
		Meta: map[string]string{
			"eventId":   "evt_" + uuid.NewString(),
			"messageId": "msgid_" + uuid.NewString(),
		},
	}
}

func signSvix(secret, id, ts string, body []byte) string {
	signed := id + "." + ts + "." + string(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signed))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func (s *server) handleEscalations(w http.ResponseWriter, r *http.Request) {
	upstream, err := http.NewRequestWithContext(r.Context(), http.MethodGet,
		s.cfg.serviceURL+"/escalations", nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	resp, err := http.DefaultClient.Do(upstream)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (s *server) handleConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	writeJSON(w, http.StatusOK, map[string]any{
		"conversation_id": id,
		"entries":         s.logs.list(id),
	})
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

// handleSendMessage intercepts the outbound Guesty reply. The service POSTs
// here (because its GUESTY_BASE_URL points at the tester); we decode the body
// to surface it in the chat UI, then return the same shape Mockoon would.
func (s *server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	convID := r.PathValue("id")
	var body struct {
		Body string `json:"body"`
		Type string `json:"type"`
	}
	raw, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &body)

	role := "bot"
	if body.Type == "note" {
		role = "note"
	}
	s.logs.append(convID, logEntry{
		Role: role,
		Body: body.Body,
		At:   time.Now().UTC(),
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"id":             "post_" + uuid.NewString(),
		"conversationId": convID,
		"body":           body.Body,
		"type":           body.Type,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
