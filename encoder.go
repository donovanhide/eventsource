package eventsource

import (
	"compress/gzip"
	"fmt"
	"io"
	"strings"
)

var (
	encFields = []struct {
		prefix string
		value  func(Event) string
	}{
		{"id: ", Event.Id},
		{"event: ", Event.Event},
		{"data: ", Event.Data},
	}
)

type Encoder struct {
	w          io.Writer
	compressed bool
}

func NewEncoder(w io.Writer, compressed bool) *Encoder {
	if compressed {
		return &Encoder{w: gzip.NewWriter(w), compressed: true}
	}
	return &Encoder{w: w}
}

func (enc *Encoder) Encode(ev Event) error {
	for _, field := range encFields {
		prefix, value := field.prefix, field.value(ev)
		if len(value) == 0 {
			continue
		}
		value = strings.Replace(value, "\n", "\n"+prefix, -1)
		if _, err := io.WriteString(enc.w, prefix+value+"\n"); err != nil {
			return fmt.Errorf("eventsource encode: %v", err)
		}
	}
	if _, err := io.WriteString(enc.w, "\n"); err != nil {
		return fmt.Errorf("eventsource encode: %v", err)
	}
	if enc.compressed {
		return enc.w.(*gzip.Writer).Flush()
	}
	return nil
}
