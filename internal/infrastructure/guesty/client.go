package guesty

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sony/gobreaker/v2"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/mappers"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
)

// Compile-time assertion that *Client satisfies the exported GuestyClient
// contract; a signature drift on either side becomes a build error.
var _ repository.GuestyClient = (*Client)(nil)

// Client is the production GuestyClient — a thin HTTP wrapper that maps all
// responses into domain types before handing them back to the caller. Paths
// mirror Guesty's real Open API; the Mockoon env in fixtures/mockoon/ follows
// the same shapes so local dev and production use identical routes.
type Client struct {
	baseURL     string
	token       string
	httpClient  *http.Client
	retries     int
	baseBackoff time.Duration
	breaker     *gobreaker.CircuitBreaker[any]
}

// ErrCircuitOpen is returned when the breaker has tripped and is refusing
// downstream calls to fail fast. Callers can errors.Is-match this sentinel to
// distinguish "Guesty is down" from a transport/status error that might
// succeed on the next request. Wraps gobreaker.ErrOpenState so existing
// gobreaker-aware telemetry keeps working.
var ErrCircuitOpen = fmt.Errorf("guesty circuit breaker open: %w", gobreaker.ErrOpenState)

// Option mutates a Client after the defaults are applied. Use WithHTTPClient
// to inject a pre-wrapped transport (e.g. otelhttp) so outgoing calls are
// traced.
type Option func(*Client)

// WithHTTPClient replaces the default HTTP client. Passing nil is a no-op —
// the default http.Client is retained.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

// WithCircuitBreaker installs a custom breaker. Passing nil disables the
// breaker entirely, which is useful in tests that do not want fail-fast
// behavior after repeated synthetic errors. The default breaker (installed by
// NewClient when this option is absent) trips after 5 consecutive post-retry
// failures, stays open for 30s, then allows one probe request.
func WithCircuitBreaker(b *gobreaker.CircuitBreaker[any]) Option {
	return func(c *Client) {
		c.breaker = b
	}
}

// defaultBreaker returns the production breaker: 5 consecutive failures trips
// it, 30s cooling period before half-open, 1 probe request while half-open.
// Counts reset every 60s while closed so a slow trickle of sporadic errors
// never accumulates toward tripping.
func defaultBreaker() *gobreaker.CircuitBreaker[any] {
	return gobreaker.NewCircuitBreaker[any](gobreaker.Settings{
		Name:        "guesty",
		MaxRequests: 1,
		Interval:    60 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(c gobreaker.Counts) bool {
			return c.ConsecutiveFailures >= 5
		},
	})
}

// NewClient constructs a Client against baseURL using token for bearer auth.
// timeout applies per-request; retries shapes how many extra attempts are made
// on 429 / 5xx / transport errors (0 = no retries, only the initial attempt).
func NewClient(baseURL, token string, timeout time.Duration, retries int, opts ...Option) *Client {
	c := &Client{
		baseURL:     baseURL,
		token:       token,
		httpClient:  &http.Client{Timeout: timeout},
		retries:     retries,
		baseBackoff: 200 * time.Millisecond,
		breaker:     defaultBreaker(),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// GetListing GETs /listings/{id} and maps the response into a domain.Listing.
// Guesty returns `houseRules` as a single string; we split on newlines so the
// domain type keeps its []string shape.
func (c *Client) GetListing(ctx context.Context, id string) (domain.Listing, error) {
	var wire wireListing
	if err := c.do(ctx, http.MethodGet, "/listings/"+url.PathEscape(id), nil, &wire); err != nil {
		return domain.Listing{}, err
	}
	return mappers.ListingFromGuesty(mappers.GuestyListingDTO{
		ID:           wire.ID,
		Title:        listingTitle(wire),
		Bedrooms:     wire.Bedrooms,
		Beds:         wire.Beds,
		MaxGuests:    wire.Accommodates,
		Amenities:    wire.Amenities,
		HouseRules:   splitRules(wire.HouseRules),
		BasePrice:    wire.Prices.BasePrice,
		Neighborhood: wire.Address.Neighborhood,
	}), nil
}

// CheckAvailability GETs the calendar endpoint for the listing over
// [from, to) and aggregates the per-day response into a domain.Availability.
// Guesty's `endDate` is inclusive, so we subtract one day from `to`
// (check-out), since the guest does not pay for the check-out night.
func (c *Client) CheckAvailability(
	ctx context.Context, listingID string, from, to time.Time,
) (domain.Availability, error) {
	endInclusive := to.Add(-24 * time.Hour)
	q := url.Values{}
	q.Set("startDate", from.Format("2006-01-02"))
	q.Set("endDate", endInclusive.Format("2006-01-02"))
	path := "/availability-pricing/api/calendar/listings/" + url.PathEscape(listingID) + "?" + q.Encode()
	var wire wireCalendar
	if err := c.do(ctx, http.MethodGet, path, nil, &wire); err != nil {
		return domain.Availability{}, err
	}
	return mappers.AvailabilityFromGuesty(aggregateCalendar(wire)), nil
}

// GetConversationHistory GETs the posts list for a conversation and returns
// up to `limit` messages. Guesty paginates with skip/limit rather than a
// cursor, so beforePostID is not wired through — the service re-fetches with
// a larger limit if the generator needs older context.
func (c *Client) GetConversationHistory(
	ctx context.Context, convID string, limit int, _ string,
) ([]domain.Message, error) {
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("skip", "0")
	path := "/communication/conversations/" + url.PathEscape(convID) + "/posts?" + q.Encode()
	var wire wirePostsResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &wire); err != nil {
		return nil, err
	}
	return mapPosts(wire.Results), nil
}

// GetConversation returns the current Conversation snapshot. The real API
// splits the conversation object from its posts across two endpoints; we
// fetch both and assemble the combined domain type so callers stay oblivious.
func (c *Client) GetConversation(ctx context.Context, convID string) (domain.Conversation, error) {
	var wire wireConversation
	path := "/communication/conversations/" + url.PathEscape(convID)
	if err := c.do(ctx, http.MethodGet, path, nil, &wire); err != nil {
		return domain.Conversation{}, err
	}
	thread, err := c.GetConversationHistory(ctx, convID, defaultThreadPageSize, "")
	if err != nil {
		return domain.Conversation{}, err
	}
	return conversationFromWire(wire, thread), nil
}

// PostNote POSTs /communication/conversations/{id}/send-message with
// type="note". Internal notes never reach the guest — this is the only send
// mode used by the service.
func (c *Client) PostNote(ctx context.Context, conversationID, body string) error {
	payload := mappers.NoteFromDomain(body)
	path := "/communication/conversations/" + url.PathEscape(conversationID) + "/send-message"
	return c.do(ctx, http.MethodPost, path, payload, nil)
}

// CreateReservation POSTs /reservations with the hold payload. The caller
// decides whether the hold is a soft inquiry or a calendar-blocking reserved
// state via in.Status; the client never promotes to "confirmed" on its own
// because an auto-confirmed booking is a human commitment by policy.
func (c *Client) CreateReservation(
	ctx context.Context, in domain.ReservationHoldInput,
) (domain.ReservationHoldResult, error) {
	if in.ListingID == "" {
		return domain.ReservationHoldResult{}, fmt.Errorf("reservation hold: missing listing_id")
	}
	if in.CheckIn.IsZero() || in.CheckOut.IsZero() {
		return domain.ReservationHoldResult{}, fmt.Errorf("reservation hold: missing check-in/out")
	}
	status := in.Status
	if status == "" {
		status = domain.ReservationInquiry
	}
	req := wireReservationRequest{
		ListingID:             in.ListingID,
		CheckInDateLocalized:  in.CheckIn.UTC().Format("2006-01-02"),
		CheckOutDateLocalized: in.CheckOut.UTC().Format("2006-01-02"),
		Status:                string(status),
		GuestsCount:           in.GuestCount,
		GuestID:               in.GuestID,
		Source:                "inquiryiq-bot",
	}
	if in.GuestID == "" && (in.GuestName != "" || in.GuestEmail != "") {
		req.Guest = &wireReservationGuest{FullName: in.GuestName, Email: in.GuestEmail}
	}
	var wire wireReservationResponse
	if err := c.do(ctx, http.MethodPost, "/reservations", req, &wire); err != nil {
		return domain.ReservationHoldResult{}, err
	}
	resp := domain.ReservationHoldResult{
		ID:               wire.ID,
		Status:           domain.ReservationHoldStatus(wire.Status),
		CheckIn:          wire.CheckIn,
		CheckOut:         wire.CheckOut,
		ConfirmationCode: wire.ConfirmationCode,
	}
	if resp.Status == "" {
		resp.Status = status
	}
	if resp.CheckIn.IsZero() {
		resp.CheckIn = in.CheckIn
	}
	if resp.CheckOut.IsZero() {
		resp.CheckOut = in.CheckOut
	}
	return resp, nil
}

// defaultThreadPageSize matches the ThreadContextWindow default in config.
// When the generator needs more, it invokes the get_conversation_history tool
// with a larger limit rather than refetching GetConversation.
const defaultThreadPageSize = 25

func listingTitle(w wireListing) string {
	if w.Title != "" {
		return w.Title
	}
	return w.Nickname
}

func splitRules(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	out := make([]string, 0, len(parts))
	for i := range parts {
		p := strings.TrimSpace(parts[i])
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// aggregateCalendar folds the per-day calendar payload into the flat domain
// projection. Available = every day's status is "available"; total sums the
// per-night price; nights = len(days).
func aggregateCalendar(w wireCalendar) mappers.GuestyAvailabilityDTO {
	days := w.Data.Days
	if len(days) == 0 {
		return mappers.GuestyAvailabilityDTO{}
	}
	available := true
	var total float64
	for i := range days {
		if days[i].Status != "available" {
			available = false
		}
		total += days[i].Price
	}
	return mappers.GuestyAvailabilityDTO{
		Available: available,
		Nights:    len(days),
		TotalUSD:  total,
	}
}

func mapPosts(posts []wirePost) []domain.Message {
	out := make([]domain.Message, 0, len(posts))
	for i := range posts {
		out = append(out, mappers.MessageFromGuesty(mappers.GuestyMessageDTO{
			PostID:    posts[i].effectivePostID(),
			Body:      posts[i].Body,
			CreatedAt: posts[i].CreatedAt,
			Type:      posts[i].Type,
			Module:    posts[i].Module,
		}))
	}
	return out
}

func conversationFromWire(wire wireConversation, thread []domain.Message) domain.Conversation {
	conv := domain.Conversation{
		RawID:       wire.ID,
		GuestID:     wire.GuestID,
		Language:    wire.Language,
		GuestName:   wire.Meta.GuestName,
		Integration: domain.Integration{Platform: wire.Integration.Platform},
		Thread:      thread,
	}
	if len(wire.Meta.Reservations) > 0 {
		conv.Reservations = make([]domain.Reservation, 0, len(wire.Meta.Reservations))
		for _, r := range wire.Meta.Reservations {
			conv.Reservations = append(conv.Reservations, domain.Reservation{
				ID:               r.ID,
				CheckIn:          r.CheckIn,
				CheckOut:         r.CheckOut,
				ConfirmationCode: r.ConfirmationCode,
			})
		}
	}
	return conv
}

// do executes a JSON request with retry on 429/5xx and transport errors. On
// exhaustion the returned error wraps both ErrRetriesExhausted and the last
// observed cause (transport error or synthesized status error), so callers
// can inspect with errors.Is / errors.As.
//
// When a circuit breaker is configured (the default), the whole retry loop
// runs inside breaker.Execute: a success closes the breaker, a terminal
// failure after retries is counted toward tripping. Once open, further calls
// return ErrCircuitOpen immediately without touching the network.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	// any: body is any JSON-marshalable Go value; out is a user-supplied
	// pointer. Both are JSON-boundary use cases permitted by conventions.
	bodyBytes, err := marshalBody(body)
	if err != nil {
		return err
	}
	work := func() (any, error) {
		return nil, c.doWithRetries(ctx, method, path, bodyBytes, out)
	}
	if c.breaker == nil {
		_, err := work()
		return err
	}
	_, err = c.breaker.Execute(work)
	if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
		return fmt.Errorf("%w: %s %s", ErrCircuitOpen, method, path)
	}
	return err
}

func (c *Client) doWithRetries(ctx context.Context, method, path string, bodyBytes []byte, out any) error {
	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		resp, sendErr := c.sendOnce(ctx, method, path, bodyBytes)
		wait, done, attemptErr := c.handleAttempt(resp, sendErr, out, attempt, method, path)
		if done {
			return attemptErr
		}
		lastErr = attemptErr
		if attempt == c.retries {
			break
		}
		if werr := waitOrCancel(ctx, wait); werr != nil {
			return werr
		}
	}
	if lastErr == nil {
		return ErrRetriesExhausted
	}
	return fmt.Errorf("%w: %w", ErrRetriesExhausted, lastErr)
}

func marshalBody(body any) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return b, nil
}

func (c *Client) sendOnce(ctx context.Context, method, path string, bodyBytes []byte) (*http.Response, error) {
	var reader io.Reader
	if bodyBytes != nil {
		reader = bytes.NewReader(bodyBytes)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	return c.httpClient.Do(req)
}

// handleAttempt inspects the outcome of one attempt. It returns the wait
// duration before the next attempt (if retrying), a done flag signaling the
// caller to stop looping, and the error for this attempt. When done is true
// the error is terminal; when done is false the error is the "last cause" for
// exhaustion reporting.
func (c *Client) handleAttempt(
	resp *http.Response, sendErr error, out any, attempt int, method, path string,
) (time.Duration, bool, error) {
	if sendErr != nil {
		cause := fmt.Errorf("guesty %s %s: %w", method, path, sendErr)
		wait := shouldRetry(nil, attempt, c.baseBackoff)
		if wait == 0 {
			return 0, true, cause
		}
		return wait, false, cause
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return 0, true, decodeInto(resp, out)
	}
	cause := fmt.Errorf("guesty %s %s: status %d", method, path, resp.StatusCode)
	wait := shouldRetry(resp, attempt, c.baseBackoff)
	_ = resp.Body.Close()
	if wait == 0 {
		return 0, true, cause
	}
	return wait, false, cause
}

func decodeInto(resp *http.Response, out any) error {
	defer func() { _ = resp.Body.Close() }()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func waitOrCancel(ctx context.Context, wait time.Duration) error {
	if wait <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		return nil
	}
}
