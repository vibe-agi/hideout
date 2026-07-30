package query

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

const (
	cursorSchema    = "hideout.activity-cursor.v1"
	cursorPrefix    = "cur_"
	maxCursorLength = 4096
)

type eventFilter struct {
	SessionID  string    `json:"sessionId,omitempty"`
	From       time.Time `json:"from,omitempty"`
	To         time.Time `json:"to,omitempty"`
	Kinds      []string  `json:"kinds,omitempty"`
	Operations []string  `json:"operations,omitempty"`
	Executions []string  `json:"executions,omitempty"`
	Risks      []string  `json:"risks,omitempty"`
	Path       string    `json:"path,omitempty"`
	Domain     string    `json:"domain,omitempty"`
	IP         string    `json:"ip,omitempty"`
}

type eventCursor struct {
	Schema   string      `json:"schema"`
	OwnerKey string      `json:"ownerKey"`
	Revision string      `json:"revision"`
	Filter   eventFilter `json:"filter"`
	Offset   int         `json:"offset"`
}

func (service *Service) encodeCursor(cursor eventCursor) (string, error) {
	cursor.Schema = cursorSchema
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", errors.Join(ErrCursorInvalid, err)
	}
	signature := hmac.New(sha256.New, service.cursorKey)
	_, _ = signature.Write(payload)
	token := cursorPrefix +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(signature.Sum(nil))
	if len(token) > maxCursorLength {
		return "", ErrCursorInvalid
	}
	return token, nil
}

func (service *Service) decodeCursor(value string) (eventCursor, error) {
	if service == nil || len(value) > maxCursorLength ||
		!strings.HasPrefix(value, cursorPrefix) {
		return eventCursor{}, ErrCursorInvalid
	}
	parts := strings.Split(strings.TrimPrefix(value, cursorPrefix), ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return eventCursor{}, ErrCursorInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(payload) == 0 || len(payload) > maxCursorLength {
		return eventCursor{}, ErrCursorInvalid
	}
	if base64.RawURLEncoding.EncodeToString(payload) != parts[0] {
		return eventCursor{}, ErrCursorInvalid
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(provided) != sha256.Size {
		return eventCursor{}, ErrCursorInvalid
	}
	if base64.RawURLEncoding.EncodeToString(provided) != parts[1] {
		return eventCursor{}, ErrCursorInvalid
	}
	expected := hmac.New(sha256.New, service.cursorKey)
	_, _ = expected.Write(payload)
	if !hmac.Equal(provided, expected.Sum(nil)) {
		return eventCursor{}, ErrCursorInvalid
	}
	var cursor eventCursor
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return eventCursor{}, ErrCursorInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return eventCursor{}, ErrCursorInvalid
	}
	if cursor.Schema != cursorSchema || cursor.OwnerKey == "" ||
		!revisionPattern.MatchString(cursor.Revision) ||
		cursor.Offset < 0 || cursor.Offset > maxSnapshotRecords {
		return eventCursor{}, ErrCursorInvalid
	}
	normalized, err := normalizeEventFilter(cursor.Filter)
	if err != nil || !equalEventFilter(normalized, cursor.Filter) {
		return eventCursor{}, ErrCursorInvalid
	}
	cursor.Filter = normalized
	return cursor, nil
}

func equalEventFilter(left, right eventFilter) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}
