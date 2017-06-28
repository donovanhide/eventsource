package eventsource

import (
	"reflect"
	"strings"
	"testing"
)

func TestDecode(t *testing.T) {
	tests := []struct {
		rawInput    string
		wantedEvent *publication
	}{
		{
			rawInput:    "event: eventName\ndata: {\"sample\":\"value\"}\n\n",
			wantedEvent: &publication{event: "eventName", data: "{\"sample\":\"value\"}"},
		},
		{
			// the newlines should not be parsed as empty event
			rawInput:    "\n\n\nevent: eventName\n\n",
			wantedEvent: &publication{event: "eventName"},
		},
	}

	for _, test := range tests {
		decoder := NewDecoder(strings.NewReader(test.rawInput))
		event, err := decoder.Decode()
		if err != nil {
			t.Fatalf("Unexpected error on decoding event: %s", err)
		}

		if !reflect.DeepEqual(event, test.wantedEvent) {
			t.Errorf("Parsed event %+v does not equal wanted event %+v", event, test.wantedEvent)
		}
	}
}
