package message

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mmrzaf/sms-gatway/internal/segment"
)

// Field error codes, shared with the API error envelope.
const (
	CodeRequired      = "required"
	CodeInvalidFormat = "invalid_format"
	CodeOutOfRange    = "out_of_range"
	CodeTooManyItems  = "too_many_items"
	CodeDuplicate     = "duplicate"
	CodeTextTooLong   = "text_too_long"
	CodeInvalidText   = "invalid_text"
	CodeConflict      = "conflict"
)

// MaxBatchSize is the largest number of messages in one batch.
const MaxBatchSize = 500

var (
	recipientPattern = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)
	clientRefPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)
)

// ValidRecipient reports whether s is an E.164 phone number.
func ValidRecipient(s string) bool { return recipientPattern.MatchString(s) }

// ValidClientRef reports whether s is a well-formed client_ref.
func ValidClientRef(s string) bool { return clientRefPattern.MatchString(s) }

// FieldError describes one invalid field.
type FieldError struct {
	Field   string
	Code    string
	Message string
}

// ValidationError lists every invalid field of a request.
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		parts[i] = f.Field + ": " + f.Message
	}
	return "invalid request: " + strings.Join(parts, "; ")
}

// ConflictError means one or more client_refs were already used by requests
// that differ from this one. Fields names each conflicting client_ref.
type ConflictError struct {
	Fields []FieldError
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("client_ref conflict on %d item(s)", len(e.Fields))
}

// Request is a message submitted by a customer.
type Request struct {
	To   string
	Text string
	// Type is the service class; empty means Normal.
	Type Type
	// ClientRef makes the submission idempotent; nil means none.
	ClientRef *string
}

// item is a validated and priced request.
type item struct {
	Request
	ref  string // the client_ref, or "" for none
	seg  segment.Result
	cost int64
}

// prepare validates and prices requests. In a batch, field names carry the
// item index, as in "messages[2].to".
func (s *Service) prepare(reqs []Request, batch bool) ([]item, error) {
	var problems []FieldError
	add := func(i int, field, code, msg string) {
		if batch {
			field = fmt.Sprintf("messages[%d].%s", i, field)
		}
		problems = append(problems, FieldError{Field: field, Code: code, Message: msg})
	}

	if batch {
		switch {
		case len(reqs) == 0:
			return nil, &ValidationError{Fields: []FieldError{{"messages", CodeRequired, "must contain at least one message"}}}
		case len(reqs) > MaxBatchSize:
			return nil, &ValidationError{Fields: []FieldError{{"messages", CodeTooManyItems,
				fmt.Sprintf("must contain at most %d messages", MaxBatchSize)}}}
		}
	}

	items := make([]item, len(reqs))
	refs := make(map[string]int)
	for i, r := range reqs {
		if r.Type == "" {
			r.Type = Normal
		}
		items[i].Request = r

		switch {
		case r.To == "":
			add(i, "to", CodeRequired, "is required")
		case !ValidRecipient(r.To):
			add(i, "to", CodeInvalidFormat, "must be an E.164 phone number such as +989121234567")
		}

		if _, ok := ParseType(string(r.Type)); !ok {
			add(i, "type", CodeInvalidFormat, "must be normal or express")
		}

		switch {
		case r.Text == "":
			add(i, "text", CodeRequired, "is required")
		case !utf8.ValidString(r.Text) || strings.ContainsRune(r.Text, 0):
			add(i, "text", CodeInvalidText, "must be valid UTF-8 without NUL characters")
		default:
			items[i].seg = segment.Count(r.Text)
			if items[i].seg.Segments > s.cfg.MaxSegments {
				add(i, "text", CodeTextTooLong,
					fmt.Sprintf("occupies %d segments; the maximum is %d", items[i].seg.Segments, s.cfg.MaxSegments))
			}
		}

		if r.ClientRef != nil {
			ref := *r.ClientRef
			if !ValidClientRef(ref) {
				add(i, "client_ref", CodeInvalidFormat, "must be 1-64 characters from A-Z a-z 0-9 . _ : -")
			} else if first, dup := refs[ref]; dup {
				add(i, "client_ref", CodeDuplicate, fmt.Sprintf("repeats the client_ref of message %d", first))
			} else {
				refs[ref] = i
				items[i].ref = ref
			}
		}

		items[i].cost = s.cfg.Prices.Cost(r.Type, items[i].seg.Segments)
	}

	if len(problems) > 0 {
		return nil, &ValidationError{Fields: problems}
	}
	return items, nil
}
