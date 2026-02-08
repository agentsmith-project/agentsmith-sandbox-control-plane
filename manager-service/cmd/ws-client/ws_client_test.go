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
func TestResizeStateUpdate(t *testing.T) {
	var st resizeState
	if _, ok, _ := st.Update(80, 24); !ok {
		t.Fatal("expected first update to emit resize")
	}
	if _, ok, _ := st.Update(80, 24); ok {
		t.Fatal("did not expect resize on same size")
	}
	if _, ok, _ := st.Update(100, 40); !ok {
		t.Fatal("expected resize on size change")
	}
}

func TestBackoffSequence(t *testing.T) {
	seq := backoffDurations(3)
	if len(seq) != 3 {
		t.Fatalf("bad len: %d", len(seq))
	}
}
