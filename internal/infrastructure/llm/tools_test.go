package llm_test

import (
	"encoding/json"
	"testing"

	"github.com/chaustre/inquiryiq/internal/infrastructure/llm"
)

func TestToolsMarshal(t *testing.T) {
	t.Parallel()
	for _, tool := range llm.AllTools {
		if tool.Function == nil || tool.Function.Name == "" {
			t.Fatalf("tool missing function: %+v", tool)
		}
		if _, err := json.Marshal(tool); err != nil {
			t.Fatalf("tool %q did not marshal: %v", tool.Function.Name, err)
		}
	}
}
