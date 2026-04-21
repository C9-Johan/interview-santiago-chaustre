package filestore

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
	"github.com/chaustre/inquiryiq/internal/domain/repository"
)

// Conversions is a JSONL-backed ConversionStore. Each call appends a full
// snapshot; Get scans the file and returns the newest record for the
// reservation. Writes fsync so shutdown before the next tick still persists.
type Conversions struct {
	mu     sync.Mutex
	path   string
	writer *os.File
}

// NewConversions opens <dir>/conversions.jsonl in append mode.
func NewConversions(dir string) (*Conversions, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	p := filepath.Join(dir, "conversions.jsonl")
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", p, err)
	}
	return &Conversions{path: p, writer: f}, nil
}

// Close flushes and closes the writer.
func (c *Conversions) Close() error {
	if err := c.writer.Close(); err != nil {
		return fmt.Errorf("close conversions: %w", err)
	}
	return nil
}

// MarkManaged appends a new managed record.
func (c *Conversions) MarkManaged(_ context.Context, r domain.ManagedReservation) error {
	if r.Status == "" {
		r.Status = "managed"
	}
	return c.append(r)
}

// RecordConversion appends a terminal snapshot carrying the new status and
// ConvertedAt timestamp.
func (c *Conversions) RecordConversion(ctx context.Context, reservationID, status string, at time.Time) error {
	prior, err := c.GetManaged(ctx, reservationID)
	if err != nil {
		return err
	}
	prior.Status = status
	prior.ConvertedAt = &at
	return c.append(prior)
}

// GetManaged scans the log and returns the latest record matching
// reservationID or ErrNotFound.
func (c *Conversions) GetManaged(_ context.Context, reservationID string) (domain.ManagedReservation, error) {
	records, err := c.scan()
	if err != nil {
		return domain.ManagedReservation{}, err
	}
	var match domain.ManagedReservation
	var found bool
	for i := range records {
		if records[i].ReservationID == reservationID {
			match = records[i]
			found = true
		}
	}
	if !found {
		return domain.ManagedReservation{}, fmt.Errorf("%w: reservationID=%s", ErrNotFound, reservationID)
	}
	return match, nil
}

// List returns up to limit most-recent records (newest first, by ManagedAt).
func (c *Conversions) List(_ context.Context, limit int) ([]domain.ManagedReservation, error) {
	records, err := c.scan()
	if err != nil {
		return nil, err
	}
	dedup := make(map[string]domain.ManagedReservation, len(records))
	for i := range records {
		dedup[records[i].ReservationID] = records[i]
	}
	out := make([]domain.ManagedReservation, 0, len(dedup))
	for _, r := range dedup {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ManagedAt.After(out[j].ManagedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

var _ repository.ConversionStore = (*Conversions)(nil)

func (c *Conversions) append(r domain.ManagedReservation) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal conversion: %w", err)
	}
	if _, err := c.writer.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write conversion: %w", err)
	}
	if err := c.writer.Sync(); err != nil {
		return fmt.Errorf("sync conversion: %w", err)
	}
	return nil
}

func (c *Conversions) scan() ([]domain.ManagedReservation, error) {
	f, err := os.Open(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open %s: %w", c.path, err)
	}
	defer func() { _ = f.Close() }()
	var out []domain.ManagedReservation
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var r domain.ManagedReservation
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan conversions: %w", err)
	}
	return out, nil
}
