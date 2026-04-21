package guesty

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/mappers"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
)

// Compile-time assertion that *Client satisfies the exported GuestyClient
// contract; a signature drift on either side becomes a build error.
var _ repository.GuestyClient = (*Client)(nil)

// Client is the production GuestyClient — a thin HTTP wrapper that maps all
// responses into domain types before handing them back to the caller.
type Client struct {
	baseURL     string
	token       string
	httpClient  *http.Client
	retries     int
	baseBackoff time.Duration
}

// NewClient constructs a Client against baseURL using token for bearer auth.
// timeout applies per-request; retries shapes how many extra attempts are made
// on 429 / 5xx / transport errors (0 = no retries, only the initial attempt).
func NewClient(baseURL, token string, timeout time.Duration, retries int) *Client {
	return &Client{
		baseURL:     baseURL,
		token:       token,
		httpClient:  &http.Client{Timeout: timeout},
		retries:     retries,
		baseBackoff: 200 * time.Millisecond,
	}
}

// GetListing GETs /listings/{id} and maps the response into a domain.Listing.
func (c *Client) GetListing(ctx context.Context, id string) (domain.Listing, error) {
	var wire wireListing
	if err := c.do(ctx, http.MethodGet, "/listings/"+url.PathEscape(id), nil, &wire); err != nil {
		return domain.Listing{}, err
	}
	return mappers.ListingFromGuesty(mappers.GuestyListingDTO{
		ID:           wire.ID,
		Title:        wire.Title,
		Bedrooms:     wire.Bedrooms,
		Beds:         wire.Beds,
		MaxGuests:    wire.MaxGuests,
		Amenities:    wire.Amenities,
		HouseRules:   wire.HouseRules,
		BasePrice:    wire.BasePrice,
		Neighborhood: wire.Neighborhood,
	}), nil
}

// CheckAvailability GETs /availability?listingId=&from=&to= and maps the
// response. Dates are serialized as YYYY-MM-DD (Guesty accepts either full
// RFC3339 or date-only; date-only keeps the URL short and unambiguous).
func (c *Client) CheckAvailability(
	ctx context.Context, listingID string, from, to time.Time,
) (domain.Availability, error) {
	q := url.Values{}
	q.Set("listingId", listingID)
	q.Set("from", from.Format("2006-01-02"))
	q.Set("to", to.Format("2006-01-02"))
	var wire wireAvailability
	if err := c.do(ctx, http.MethodGet, "/availability?"+q.Encode(), nil, &wire); err != nil {
		return domain.Availability{}, err
	}
	return mappers.AvailabilityFromGuesty(mappers.GuestyAvailabilityDTO{
		Available: wire.Available,
		Nights:    wire.Nights,
		TotalUSD:  wire.Total,
	}), nil
}

// GetConversationHistory GETs /conversations/{id}/messages?limit=&before=.
// When beforePostID is empty the parameter is omitted.
func (c *Client) GetConversationHistory(
	ctx context.Context, convID string, limit int, beforePostID string,
) ([]domain.Message, error) {
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	if beforePostID != "" {
		q.Set("before", beforePostID)
	}
	path := "/conversations/" + url.PathEscape(convID) + "/messages?" + q.Encode()
	var wire []wireMessage
	if err := c.do(ctx, http.MethodGet, path, nil, &wire); err != nil {
		return nil, err
	}
	return mapMessages(wire), nil
}

// GetConversation returns the current Conversation snapshot.
func (c *Client) GetConversation(ctx context.Context, convID string) (domain.Conversation, error) {
	var wire wireConversationResponse
	if err := c.do(ctx, http.MethodGet, "/conversations/"+url.PathEscape(convID), nil, &wire); err != nil {
		return domain.Conversation{}, err
	}
	return conversationFromWire(wire), nil
}

// PostNote POSTs /conversations/{id}/messages with type="note". Internal
// notes never reach the guest — this is the only send mode used by the
// service.
func (c *Client) PostNote(ctx context.Context, conversationID, body string) error {
	payload := mappers.NoteFromDomain(body)
	path := "/conversations/" + url.PathEscape(conversationID) + "/messages"
	return c.do(ctx, http.MethodPost, path, payload, nil)
}

func mapMessages(wire []wireMessage) []domain.Message {
	out := make([]domain.Message, 0, len(wire))
	for i := range wire {
		out = append(out, mappers.MessageFromGuesty(mappers.GuestyMessageDTO{
			PostID:    wire[i].PostID,
			Body:      wire[i].Body,
			CreatedAt: wire[i].CreatedAt,
			Type:      wire[i].Type,
			Module:    wire[i].Module,
		}))
	}
	return out
}

func conversationFromWire(wire wireConversationResponse) domain.Conversation {
	conv := domain.Conversation{
		RawID:       wire.ID,
		GuestID:     wire.GuestID,
		Language:    wire.Language,
		GuestName:   wire.Meta.GuestName,
		Integration: domain.Integration{Platform: wire.Integration.Platform},
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
	conv.Thread = mapMessages(wire.Thread)
	return conv
}

// do executes a JSON request with retry on 429/5xx and transport errors.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	// any: body is any JSON-marshalable Go value; out is a user-supplied
	// pointer. Both are JSON-boundary use cases permitted by conventions.
	bodyBytes, err := marshalBody(body)
	if err != nil {
		return err
	}
	var lastStatus int
	for attempt := 0; attempt <= c.retries; attempt++ {
		resp, sendErr := c.sendOnce(ctx, method, path, bodyBytes)
		wait, done, doneErr := c.handleAttempt(resp, sendErr, out, attempt)
		if done {
			return doneErr
		}
		lastStatus = 0
		if resp != nil {
			lastStatus = resp.StatusCode
		}
		if werr := waitOrCancel(ctx, wait); werr != nil {
			return werr
		}
	}
	return fmt.Errorf("%w: last status %d", ErrRetriesExhausted, lastStatus)
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
// caller to stop looping, and the error to surface when done.
func (c *Client) handleAttempt(
	resp *http.Response, sendErr error, out any, attempt int,
) (time.Duration, bool, error) {
	if sendErr != nil {
		wait := shouldRetry(nil, attempt, c.baseBackoff)
		if wait == 0 {
			return 0, true, fmt.Errorf("guesty request: %w", sendErr)
		}
		return wait, false, nil
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return 0, true, decodeInto(resp, out)
	}
	wait := shouldRetry(resp, attempt, c.baseBackoff)
	_ = resp.Body.Close()
	if wait == 0 {
		return 0, true, fmt.Errorf("guesty: status %d", resp.StatusCode)
	}
	return wait, false, nil
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
