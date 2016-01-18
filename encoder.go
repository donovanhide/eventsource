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

type encoder struct {
	w          io.Writer
	compressed bool
}

func newEncoder(w io.Writer, compressed bool) *encoder {
	if compressed {
		return &encoder{w: gzip.NewWriter(w), compressed: true}
	}
	return &encoder{w: w}
}

func (enc *encoder) Encode(ev Event) error {
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
