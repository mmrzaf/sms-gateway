package message

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestLane(t *testing.T) {
	id := uuid.New()
	if got := Lane(Express, id, 16); got != ExpressLane {
		t.Errorf("express lane = %q", got)
	}
	first := Lane(Normal, id, 16)
	if !strings.HasPrefix(first, "normal-") {
		t.Fatalf("normal lane = %q", first)
	}
	if again := Lane(Normal, id, 16); again != first {
		t.Errorf("lane not stable: %q then %q", first, again)
	}
	seen := make(map[string]bool)
	for range 2000 {
		seen[Lane(Normal, uuid.New(), 16)] = true
	}
	if len(seen) != 16 {
		t.Errorf("2000 customers spread over %d lanes, want 16", len(seen))
	}
	if got := Lane(Normal, id, 1); got != "normal-0" {
		t.Errorf("single lane = %q", got)
	}
}

func TestPrices(t *testing.T) {
	p := Prices{Normal: 1, Express: 3}
	if got := p.Cost(Normal, 2); got != 2 {
		t.Errorf("normal cost = %d", got)
	}
	if got := p.Cost(Express, 2); got != 6 {
		t.Errorf("express cost = %d", got)
	}
}

func TestStatuses(t *testing.T) {
	for _, st := range Statuses {
		if got, ok := ParseStatus(string(st)); !ok || got != st {
			t.Errorf("ParseStatus(%q) = %q, %v", st, got, ok)
		}
	}
	if _, ok := ParseStatus("queued"); ok {
		t.Error("unknown status accepted")
	}
	terminal := map[Status]bool{StatusDelivered: true, StatusUndelivered: true, StatusFailed: true, StatusExpired: true}
	for _, st := range Statuses {
		if st.Terminal() != terminal[st] {
			t.Errorf("%s.Terminal() = %v", st, st.Terminal())
		}
	}
}

func ref(s string) *string { return &s }

func TestPrepareValidation(t *testing.T) {
	s := &Service{cfg: Config{Prices: Prices{Normal: 1, Express: 3}, MaxSegments: 2}}
	tests := []struct {
		name  string
		req   Request
		field string
		code  string
	}{
		{"missing to", Request{Text: "hi"}, "to", CodeRequired},
		{"bad to", Request{To: "09121234567", Text: "hi"}, "to", CodeInvalidFormat},
		{"missing text", Request{To: "+989121234567"}, "text", CodeRequired},
		{"nul in text", Request{To: "+989121234567", Text: "a\x00b"}, "text", CodeInvalidText},
		{"too long", Request{To: "+989121234567", Text: strings.Repeat("a", 307)}, "text", CodeTextTooLong},
		{"bad type", Request{To: "+989121234567", Text: "hi", Type: "urgent"}, "type", CodeInvalidFormat},
		{"empty client_ref", Request{To: "+989121234567", Text: "hi", ClientRef: ref("")}, "client_ref", CodeInvalidFormat},
		{"bad client_ref", Request{To: "+989121234567", Text: "hi", ClientRef: ref("a b")}, "client_ref", CodeInvalidFormat},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.prepare([]Request{tc.req}, false)
			v, ok := err.(*ValidationError)
			if !ok || len(v.Fields) != 1 || v.Fields[0].Field != tc.field || v.Fields[0].Code != tc.code {
				t.Fatalf("got %v, want %s/%s", err, tc.field, tc.code)
			}
		})
	}
}

func TestPrepareBatch(t *testing.T) {
	s := &Service{cfg: Config{Prices: Prices{Normal: 1, Express: 3}, MaxSegments: 10}}

	items, err := s.prepare([]Request{
		{To: "+989121234567", Text: "hello"},
		{To: "+989121234567", Text: strings.Repeat("س", 100), Type: Express},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Type != Normal || items[0].cost != 1 {
		t.Errorf("item 0: type %q cost %d", items[0].Type, items[0].cost)
	}
	if items[1].seg.Segments != 2 || items[1].cost != 6 {
		t.Errorf("item 1: %d segments cost %d", items[1].seg.Segments, items[1].cost)
	}

	_, err = s.prepare([]Request{
		{To: "+989121234567", Text: "a", ClientRef: ref("x")},
		{To: "bad", Text: "b", ClientRef: ref("x")},
	}, true)
	v, ok := err.(*ValidationError)
	if !ok || len(v.Fields) != 2 || v.Fields[0].Field != "messages[1].to" || v.Fields[1].Code != CodeDuplicate {
		t.Errorf("batch problems: %v", err)
	}

	if _, err := s.prepare(nil, true); err == nil {
		t.Error("empty batch accepted")
	}
	if _, err := s.prepare(make([]Request, MaxBatchSize+1), true); err == nil {
		t.Error("oversized batch accepted")
	}
}
