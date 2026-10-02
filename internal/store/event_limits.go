package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/jarymor-ux/kee-route-manager/internal/event"
	"github.com/jarymor-ux/kee-route-manager/internal/redact"
)

// Leave headroom for newline and the reader's buffer boundary.
const maxEventRecordSize = (1 << 20) - 1
const eventTruncation = " [diagnostic truncated: event record limit]"

func boundedEvent(e event.Event) (event.Event, []byte, error) {
	b, err := json.Marshal(e)
	if err != nil || len(b) <= maxEventRecordSize {
		return e, b, err
	}
	// Fields can contain arbitrary diagnostic payloads. Their omission, as well as
	// clipping the message, is explicit in the retained diagnostic.
	e.Fields = nil
	trim := func(s string) string {
		if len(s) > 256 {
			return s[:256]
		}
		return s
	}
	e.Level = trim(e.Level)
	e.Type = trim(e.Type)
	e.OperationID = trim(e.OperationID)
	message := e.Message
	low, high := 0, len(message)
	for low < high {
		mid := low + (high-low+1)/2
		e.Message = message[:mid] + eventTruncation
		b, err = json.Marshal(e)
		if err != nil {
			return e, nil, err
		}
		if len(b) <= maxEventRecordSize {
			low = mid
		} else {
			high = mid - 1
		}
	}
	e.Message = message[:low] + eventTruncation
	b, err = json.Marshal(e)
	return e, b, err
}

// readEventRecord retains at most one bounded record while draining an arbitrarily
// large legacy line through a fixed buffer. A bad diagnostic cannot block loading
// routing state or the journal. The prefix preserves sequence numbers written by
// prior KRM versions, which serialize sequence before diagnostic payloads.
func readEventRecord(r *bufio.Reader) ([]byte, bool, error) {
	var line []byte
	oversized := false
	for {
		part, err := r.ReadSlice('\n')
		part = bytes.TrimSuffix(part, []byte{'\n'})
		if len(part) > 0 {
			remaining := maxEventRecordSize + 1 - len(line)
			if remaining > 0 {
				if len(part) > remaining {
					line = append(line, part[:remaining]...)
				} else {
					line = append(line, part...)
				}
			}
			if len(line) > maxEventRecordSize {
				oversized = true
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return bytes.TrimSuffix(line, []byte{'\n'}), oversized, err
	}
}
func legacyTruncatedEvent(prefix []byte) event.Event {
	e := event.Event{Level: "warning", Type: "events.truncated", Message: "legacy oversized event omitted" + eventTruncation}
	dec := json.NewDecoder(bytes.NewReader(prefix))
	if token, err := dec.Token(); err != nil || token != json.Delim('{') {
		return e
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			break
		}
		if key == "sequence" {
			_ = dec.Decode(&e.Sequence)
			break
		}
		var skip json.RawMessage
		if err = dec.Decode(&skip); err != nil {
			break
		}
	}
	return e
}
func (s *Store) loadEvents(r io.Reader) error {
	reader := bufio.NewReaderSize(r, 64<<10)
	for {
		line, oversized, err := readEventRecord(reader)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if len(line) > 0 {
			var e event.Event
			valid := false
			if oversized {
				e = legacyTruncatedEvent(line)
				valid = true
			} else {
				decoder := json.NewDecoder(bytes.NewReader(line))
				decoder.UseNumber()
				valid = decoder.Decode(&e) == nil && decoder.Decode(new(any)) == io.EOF
			}
			if valid {
				e.Message = redact.Text(e.Message)
				e.Fields = redactFields(e.Fields)
				s.events = append(s.events, e)
				if e.Sequence > s.seq {
					s.seq = e.Sequence
				}
				if len(s.events) > maxEvents {
					s.events = s.events[len(s.events)-maxEvents:]
				}
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}
