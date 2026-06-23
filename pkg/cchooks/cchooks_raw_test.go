package cchooks

import (
	"strings"
	"testing"
)

func TestDecodePopulatesRaw(t *testing.T) {
	data := []byte(`{"hook_event_name":"Stop","session_id":"s1","background_tasks":[]}`)
	ev, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	stop, ok := ev.(*Stop)
	if !ok {
		t.Fatalf("got %T, want *Stop", ev)
	}
	if string(stop.Raw) != string(data) {
		t.Errorf("Raw = %q, want %q", stop.Raw, data)
	}
}

func TestDecodeUnknownEventKeepsRaw(t *testing.T) {
	// A future/unknown event decodes into *Common but must keep its body so
	// consumers can still recover the fields this library does not model.
	data := []byte(`{"hook_event_name":"FutureThing","session_id":"s1","new_field":42}`)
	ev, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := ev.(*Common)
	if !ok {
		t.Fatalf("got %T, want *Common", ev)
	}
	if !strings.Contains(string(c.Raw), "new_field") {
		t.Errorf("Raw lost the unknown field: %q", c.Raw)
	}
	if c.EventName() != "FutureThing" {
		t.Errorf("EventName = %q, want FutureThing", c.EventName())
	}
}

func TestParseRejectsOversizedPayload(t *testing.T) {
	big := strings.NewReader("{" + strings.Repeat(" ", maxPayloadBytes+10) + "}")
	if _, err := Parse(big); err == nil {
		t.Fatal("expected error for oversized payload, got nil")
	}
}
