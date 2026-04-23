package filestore

import (
	"fmt"
	"os"
)

// truncateAndReopen closes the current writer, truncates the file at path,
// and reopens it in append mode. Caller owns the store's mutex; this helper
// only handles the OS-level handoff so the demo Reset endpoint cannot leave
// a half-truncated, half-open file.
func truncateAndReopen(writer *os.File, path string) (*os.File, error) {
	if writer != nil {
		_ = writer.Close()
	}
	if err := os.Truncate(path, 0); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("truncate %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("reopen %s: %w", path, err)
	}
	return f, nil
}
