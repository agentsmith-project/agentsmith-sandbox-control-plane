package main

import "testing"

func TestEncodeResizePayload(t *testing.T) {
	msg, err := buildResizeMessage(120, 40)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Type != "resize" {
		t.Fatalf("type=%s", msg.Type)
	}
}
