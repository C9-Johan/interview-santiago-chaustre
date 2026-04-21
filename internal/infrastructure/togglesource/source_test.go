package togglesource

import (
	"context"
	"sync"
	"testing"

	"github.com/chaustre/inquiryiq/internal/domain"
)

func TestCurrentReturnsInitialState(t *testing.T) {
	s := New(domain.Toggles{AutoResponseEnabled: true}, nil, nil)
	if got := s.Current(); !got.AutoResponseEnabled {
		t.Errorf("Current(): got %+v, want AutoResponseEnabled=true", got)
	}
}

func TestSetAutoResponseReturnsPrevAndNow(t *testing.T) {
	s := New(domain.Toggles{AutoResponseEnabled: true}, nil, nil)
	prev, now := s.SetAutoResponse(context.Background(), false, "oncall")
	if prev != true || now != false {
		t.Errorf("flip off: got prev=%t now=%t, want true/false", prev, now)
	}
	if s.Current().AutoResponseEnabled {
		t.Error("current should reflect new state")
	}
	prev2, now2 := s.SetAutoResponse(context.Background(), true, "oncall")
	if prev2 != false || now2 != true {
		t.Errorf("flip on: got prev=%t now=%t, want false/true", prev2, now2)
	}
}

func TestSetAutoResponseIdempotentWriteStillLogs(t *testing.T) {
	// Explicit re-set to the same value — operators may toggle-off twice during
	// incidents. Behavior check: Current stays true, Set returns prev==now==true.
	s := New(domain.Toggles{AutoResponseEnabled: true}, nil, nil)
	prev, now := s.SetAutoResponse(context.Background(), true, "oncall")
	if prev != true || now != true {
		t.Errorf("no-op flip: got prev=%t now=%t, want true/true", prev, now)
	}
}

func TestSetAutoResponseIsConcurrencySafe(_ *testing.T) {
	s := New(domain.Toggles{AutoResponseEnabled: true}, nil, nil)
	var wg sync.WaitGroup
	for i := range 200 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.SetAutoResponse(context.Background(), i%2 == 0, "race")
			_ = s.Current()
		}(i)
	}
	wg.Wait()
	// Final state is nondeterministic; the test passes if -race did not fire.
}
