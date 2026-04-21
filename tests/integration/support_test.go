//go:build integration

// Package integration_test boots the InquiryIQ service end-to-end against
// Mockoon (for Guesty) and an in-process fake OpenAI-compatible LLM. Every
// test here skips when `mockoon-cli` is missing so CI and contributors
// without Mockoon keep moving.
package integration_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/application/classify"
	"github.com/chaustre/inquiryiq/internal/application/decide"
	"github.com/chaustre/inquiryiq/internal/application/generatereply"
	"github.com/chaustre/inquiryiq/internal/application/processinquiry"
	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
	"github.com/chaustre/inquiryiq/internal/infrastructure/clock"
	"github.com/chaustre/inquiryiq/internal/infrastructure/debouncer"
	"github.com/chaustre/inquiryiq/internal/infrastructure/guesty"
	"github.com/chaustre/inquiryiq/internal/infrastructure/llm"
	"github.com/chaustre/inquiryiq/internal/infrastructure/store/memstore"
	transporthttp "github.com/chaustre/inquiryiq/internal/transport/http"
)

const (
	webhookSecret     = "whsec_integration_test"
	mockoonPort       = 3001
	mockoonStartupTTL = 10 * time.Second
	defaultListingID  = "L1"
)

// skipIfNoMockoon skips when mockoon-cli is not on PATH. These tests parse
// Mockoon's transaction log to assert on outbound Guesty calls; without the
// log there is nothing to assert.
func skipIfNoMockoon(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("mockoon-cli"); err != nil {
		t.Skipf("mockoon-cli not on PATH: %v — run `npm i -g @mockoon/cli` to enable", err)
	}
}

// mockoonServer wraps the Mockoon subprocess plus its combined log file.
type mockoonServer struct {
	cmd     *exec.Cmd
	logFile *os.File
	logPath string
	baseURL string
}

// startMockoon launches mockoon-cli against fixtures/mockoon/guesty.json and
// redirects stdout+stderr (which includes the transaction log) into a temp
// file. Blocks until the fake listings endpoint answers.
func startMockoon(t *testing.T) *mockoonServer {
	t.Helper()
	envPath := repoPath(t, "fixtures/mockoon/guesty.json")
	logFile, err := os.CreateTemp(t.TempDir(), "mockoon-log-*.ndjson")
	if err != nil {
		t.Fatalf("create mockoon log: %v", err)
	}
	port := strconv.Itoa(mockoonPort)
	cmd := exec.Command(
		"mockoon-cli", "start", "-d", envPath, "--port", port, "--log-transaction",
	)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("start mockoon: %v", err)
	}
	srv := &mockoonServer{
		cmd:     cmd,
		logFile: logFile,
		logPath: logFile.Name(),
		baseURL: "http://127.0.0.1:" + port,
	}
	t.Cleanup(srv.stop)
	waitForHealthy(t, srv.baseURL+"/listings/"+defaultListingID, mockoonStartupTTL)
	return srv
}

func (s *mockoonServer) stop() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
	}
	if s.logFile != nil {
		_ = s.logFile.Close()
	}
}

func waitForHealthy(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, url, nil)
		if err != nil {
			cancel()
			t.Fatalf("new request: %v", err)
		}
		resp, err := nethttp.DefaultClient.Do(req)
		cancel()
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < 500 {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("mockoon did not answer %s within %s", url, timeout)
}

// openAIRequest is a minimal projection of the OpenAI chat completion request.
// The fields cover what the classifier and generator send; we ignore the rest.
type openAIRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Tools       []openAITool    `json:"tools,omitempty"`
	ToolChoice  json.RawMessage `json:"tool_choice,omitempty"`
	Temperature float64         `json:"temperature,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
}

type openAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name string `json:"name"`
	} `json:"function"`
}

type openAIResponse struct {
	ID      string         `json:"id"`
	Model   string         `json:"model"`
	Choices []openAIChoice `json:"choices"`
}

type openAIChoice struct {
	Index        int           `json:"index"`
	Message      openAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

// llmResponder is a scripted fake OpenAI-compatible server. Tests pass an
// onChat callback that inspects the incoming request and returns the response
// to send back.
type llmResponder struct {
	srv        *httptest.Server
	baseURL    string
	onChat     func(openAIRequest) openAIResponse
	mu         sync.Mutex
	transcript []openAIRequest
	calls      atomic.Int32
}

// startFakeLLM starts the responder and registers cleanup. URL() is the
// BaseURL to pass into llm.NewClient.
func startFakeLLM(t *testing.T, onChat func(openAIRequest) openAIResponse) *llmResponder {
	t.Helper()
	r := &llmResponder{onChat: onChat}
	srv := httptest.NewServer(nethttp.HandlerFunc(r.handle))
	r.srv = srv
	r.baseURL = srv.URL
	t.Cleanup(srv.Close)
	return r
}

func (r *llmResponder) URL() string { return r.baseURL }

func (r *llmResponder) Calls() int32 { return r.calls.Load() }

func (r *llmResponder) handle(w nethttp.ResponseWriter, req *nethttp.Request) {
	if req.Method != nethttp.MethodPost || !strings.HasSuffix(req.URL.Path, "/chat/completions") {
		nethttp.Error(w, "unexpected path "+req.URL.Path, nethttp.StatusNotFound)
		return
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		nethttp.Error(w, "body: "+err.Error(), nethttp.StatusBadRequest)
		return
	}
	var parsed openAIRequest
	if err := json.Unmarshal(body, &parsed); err != nil {
		nethttp.Error(w, "unmarshal: "+err.Error(), nethttp.StatusBadRequest)
		return
	}
	r.mu.Lock()
	r.transcript = append(r.transcript, parsed)
	r.mu.Unlock()
	r.calls.Add(1)
	resp := r.onChat(parsed)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// chatReplyJSON builds an assistant response carrying raw JSON as content.
// The classifier expects a single JSON object in assistant content; the
// generator expects a tool-call or terminal-text response.
func chatReplyJSON(bodyJSON string) openAIResponse {
	return openAIResponse{
		ID:    "resp-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Model: "fake-llm",
		Choices: []openAIChoice{{
			Index:        0,
			Message:      openAIMessage{Role: "assistant", Content: bodyJSON},
			FinishReason: "stop",
		}},
	}
}

// chatToolCall builds an assistant response requesting a tool call. The
// generator loops on these until it gets a terminal text response.
func chatToolCall(id, name, argsJSON string) openAIResponse {
	return openAIResponse{
		ID:    "resp-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Model: "fake-llm",
		Choices: []openAIChoice{{
			Index: 0,
			Message: openAIMessage{
				Role: "assistant",
				ToolCalls: []openAIToolCall{{
					ID:       id,
					Type:     "function",
					Function: openAIToolFunction{Name: name, Arguments: argsJSON},
				}},
			},
			FinishReason: "tool_calls",
		}},
	}
}

// hasTools returns true for generator-shaped requests (tools advertised);
// classifier requests come with no tools.
func (r openAIRequest) hasTools() bool { return len(r.Tools) > 0 }

// bootedService exposes the wiring results tests need to assert against.
type bootedService struct {
	url         string
	debouncer   *debouncer.Timed
	escalations repository.EscalationStore
	memory      repository.ConversationMemoryStore
}

// bootService builds the full application graph on top of configured Guesty
// and LLM base URLs, then wraps the router in httptest.NewServer. The flush
// callback bridges the debouncer to the orchestrator — it fetches the
// conversation snapshot via Guesty and resolves the listing id.
func bootService(
	t *testing.T,
	guestyBaseURL, llmBaseURL string,
	debounceWindow time.Duration,
) *bootedService {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	gclient := guesty.NewClient(guestyBaseURL, "dev-token", 5*time.Second, 1)
	llmClient := llm.NewClient(llmBaseURL, "fake-key")

	classifier, err := classify.New(llmClient, "fake-model", 10*time.Second)
	if err != nil {
		t.Fatalf("classify.New: %v", err)
	}
	generator := generatereply.New(llmClient, gclient, "fake-model", 10*time.Second, 4)

	idempotency := memstore.NewIdempotency()
	memory := memstore.NewConversationMemory()
	classes := &nopClassifications{}
	escRing := memstore.NewEscalationRing(100, nil)

	orch := processinquiry.New(processinquiry.Deps{
		Classifier:      classifier,
		Generator:       generator,
		Guesty:          gclient,
		Idempotency:     idempotency,
		Escalations:     escRing,
		Memory:          memory,
		Classifications: classes,
		Toggles:         domain.Toggles{AutoResponseEnabled: true},
		Thresholds:      decide.Thresholds{ClassifierMin: 0.65, GeneratorMin: 0.70},
		Log:             log,
	})

	clk := clock.NewReal()
	flush := func(ctx context.Context, turn domain.Turn) {
		conv, err := gclient.GetConversation(ctx, string(turn.Key))
		if err != nil {
			t.Logf("flush get conversation: %v", err)
			return
		}
		orch.Run(ctx, processinquiry.Input{
			Turn:         turn,
			Conversation: conv,
			ListingID:    defaultListingID,
			Now:          time.Now().UTC(),
		})
	}
	deb := debouncer.NewTimed(debounceWindow, 2*debounceWindow, clk, flush)

	handler := transporthttp.NewHandler(transporthttp.Handler{
		Webhooks:         nopWebhooks{},
		EscalationsStore: escRing,
		Idempotency:      idempotency,
		Resolver:         rawIDResolver{},
		Debouncer:        deb,
		SvixSecret:       webhookSecret,
		SvixMaxDrift:     5 * time.Minute,
		Log:              log,
	})
	router := transporthttp.NewRouter(handler, nil)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	t.Cleanup(deb.Stop)
	return &bootedService{
		url:         srv.URL,
		debouncer:   deb,
		escalations: escRing,
		memory:      memory,
	}
}

// nopClassifications swallows classification writes; integration assertions
// read escalations and outbound Guesty calls rather than the classification
// log.
type nopClassifications struct {
	puts atomic.Int32
}

func (s *nopClassifications) Put(_ context.Context, _ string, _ domain.Classification) error {
	s.puts.Add(1)
	return nil
}

func (s *nopClassifications) Get(_ context.Context, _ string) (domain.Classification, error) {
	return domain.Classification{}, nil
}

type nopWebhooks struct{}

func (nopWebhooks) Append(_ context.Context, _ repository.WebhookRecord) error { return nil }
func (nopWebhooks) Get(_ context.Context, _ string) (repository.WebhookRecord, error) {
	return repository.WebhookRecord{}, nil
}

func (nopWebhooks) Since(_ context.Context, _ time.Duration) ([]repository.WebhookRecord, error) {
	return nil, nil
}

type rawIDResolver struct{}

func (rawIDResolver) Resolve(_ context.Context, c domain.Conversation) (domain.ConversationKey, error) {
	return domain.ConversationKey(c.RawID), nil
}

// postSignedWebhook reads fixturePath, signs the raw bytes with webhookSecret,
// POSTs to svcURL, and returns the response. The signed bytes are the same
// bytes shipped — matching the Svix contract we enforce server-side.
func postSignedWebhook(t *testing.T, svcURL, fixturePath string) *nethttp.Response {
	t.Helper()
	body, err := os.ReadFile(repoPath(t, fixturePath))
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixturePath, err)
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	id := "msg-" + ts + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	sig := svixSign(webhookSecret, id, ts, body)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	req, err := nethttp.NewRequestWithContext(
		ctx, nethttp.MethodPost,
		svcURL+"/webhooks/guesty/message-received", bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("svix-id", id)
	req.Header.Set("svix-timestamp", ts)
	req.Header.Set("svix-signature", sig)
	resp, err := nethttp.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST webhook: %v", err)
	}
	return resp
}

func svixSign(secret, id, ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(id + "." + ts + "."))
	m.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(m.Sum(nil))
}

// waitForSendMessage polls Mockoon's transaction log for a POST to any
// /communication/conversations/*/send-message endpoint and returns the
// request body. Fails on timeout.
func waitForSendMessage(t *testing.T, logPath string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if body := scanForSendMessage(logPath); body != "" {
			return body
		}
		time.Sleep(200 * time.Millisecond)
	}
	dumpLog(t, logPath)
	t.Fatalf("no send-message POST observed within %s", timeout)
	return ""
}

// countSendMessage reports how many POST …/send-message transactions have
// been observed so far. Parsed structurally — strings.Contains on raw lines
// would also match bodies that quote the path.
func countSendMessage(t *testing.T, logPath string) int {
	t.Helper()
	f, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	count := 0
	for sc.Scan() {
		if isSendMessageTransaction(sc.Text()) {
			count++
		}
	}
	return count
}

func scanForSendMessage(logPath string) string {
	f, err := os.Open(logPath)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !isSendMessageTransaction(line) {
			continue
		}
		if body := extractRequestBody(line); body != "" {
			return body
		}
	}
	return ""
}

func isSendMessageTransaction(line string) bool {
	var rec mockoonLogLine
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		return false
	}
	return rec.Transaction.Request.Method == "POST" &&
		strings.HasSuffix(rec.RequestPath, "/send-message")
}

// mockoonLogLine is the subset of Mockoon's transaction log record we care
// about. The real record is much larger; unmarshaling into a narrow struct
// is safer than regexp-scanning a line that embeds an already-escaped JSON
// body (escaped quotes would fool a naive string scanner).
type mockoonLogLine struct {
	Message     string `json:"message"`
	RequestPath string `json:"requestPath"`
	Transaction struct {
		Request struct {
			Body   string `json:"body"`
			Method string `json:"method"`
		} `json:"request"`
	} `json:"transaction"`
}

// extractRequestBody decodes a Mockoon transaction log line and returns the
// inbound request body, or "" when the line is not a transaction record or
// the request body is empty.
func extractRequestBody(line string) string {
	var rec mockoonLogLine
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		return ""
	}
	if rec.Transaction.Request.Method == "" {
		return ""
	}
	return rec.Transaction.Request.Body
}

func dumpLog(t *testing.T, logPath string) {
	t.Helper()
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Logf("cannot read mockoon log: %v", err)
		return
	}
	t.Logf("--- mockoon log (%s) ---\n%s", logPath, b)
}

// repoPath resolves a path relative to the module root so tests work whether
// they run from ./... or tests/integration.
func repoPath(t *testing.T, rel string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, rel)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("cannot find go.mod above %q", dir)
		}
		dir = parent
	}
}
