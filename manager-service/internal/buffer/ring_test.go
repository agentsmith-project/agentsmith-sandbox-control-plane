package buffer

import (
	"testing"
)

func TestNewRingBuffer(t *testing.T) {
	size := 10
	rb := NewRingBuffer(size)

	if rb == nil {
		t.Fatal("NewRingBuffer returned nil")
	}
	if rb.size != size {
		t.Errorf("Expected size %d, got %d", size, rb.size)
	}
	if rb.count != 0 {
		t.Errorf("Expected count 0, got %d", rb.count)
	}
	if rb.head != 0 {
		t.Errorf("Expected head 0, got %d", rb.head)
	}
	if rb.tail != 0 {
		t.Errorf("Expected tail 0, got %d", rb.tail)
	}
}

func TestRingBuffer_Write(t *testing.T) {
	rb := NewRingBuffer(3)

	msg1 := &Message{Type: "stdout", Data: []byte("hello")}
	msg2 := &Message{Type: "stderr", Data: []byte("world")}

	rb.Write(msg1)
	rb.Write(msg2)

	if rb.count != 2 {
		t.Errorf("Expected count 2, got %d", rb.count)
	}
}

func TestRingBuffer_WriteOverwrite(t *testing.T) {
	rb := NewRingBuffer(2)

	msg1 := &Message{Type: "stdout", Data: []byte("1")}
	msg2 := &Message{Type: "stdout", Data: []byte("2")}
	msg3 := &Message{Type: "stdout", Data: []byte("3")}

	rb.Write(msg1)
	rb.Write(msg2)
	rb.Write(msg3) // Should overwrite msg1

	if rb.count != 2 {
		t.Errorf("Expected count 2, got %d", rb.count)
	}
	if rb.head != 1 {
		t.Errorf("Expected head 1 (msg2 position), got %d", rb.head)
	}
}

func TestRingBuffer_ReadAll(t *testing.T) {
	rb := NewRingBuffer(10)

	msg1 := &Message{Type: "stdout", Data: []byte("hello")}
	msg2 := &Message{Type: "stderr", Data: []byte("world")}
	msg3 := &Message{Type: "exit", ExitCode: 0}

	rb.Write(msg1)
	rb.Write(msg2)
	rb.Write(msg3)

	messages := rb.ReadAll()

	if len(messages) != 3 {
		t.Fatalf("Expected 3 messages, got %d", len(messages))
	}
	if messages[0] != msg1 {
		t.Error("First message should be msg1")
	}
	if messages[1] != msg2 {
		t.Error("Second message should be msg2")
	}
	if messages[2] != msg3 {
		t.Error("Third message should be msg3")
	}
}

func TestRingBuffer_ReadAllAfterOverwrite(t *testing.T) {
	rb := NewRingBuffer(3)

	msg1 := &Message{Type: "stdout", Data: []byte("1")}
	msg2 := &Message{Type: "stdout", Data: []byte("2")}
	msg3 := &Message{Type: "stdout", Data: []byte("3")}
	msg4 := &Message{Type: "stdout", Data: []byte("4")}

	rb.Write(msg1)
	rb.Write(msg2)
	rb.Write(msg3)
	rb.Write(msg4) // Overwrites msg1

	messages := rb.ReadAll()

	if len(messages) != 3 {
		t.Fatalf("Expected 3 messages, got %d", len(messages))
	}
	// Should contain msg2, msg3, msg4 in order
	if messages[0] != msg2 {
		t.Error("First message should be msg2")
	}
	if messages[1] != msg3 {
		t.Error("Second message should be msg3")
	}
	if messages[2] != msg4 {
		t.Error("Third message should be msg4")
	}
}

func TestRingBuffer_ReadAllEmpty(t *testing.T) {
	rb := NewRingBuffer(10)

	messages := rb.ReadAll()

	if len(messages) != 0 {
		t.Errorf("Expected 0 messages, got %d", len(messages))
	}
}

func TestRingBuffer_Clear(t *testing.T) {
	rb := NewRingBuffer(10)

	msg := &Message{Type: "stdout", Data: []byte("hello")}
	rb.Write(msg)
	rb.Write(msg)
	rb.Write(msg)

	if rb.count != 3 {
		t.Errorf("Expected count 3 before clear, got %d", rb.count)
	}

	rb.Clear()

	if rb.count != 0 {
		t.Errorf("Expected count 0 after clear, got %d", rb.count)
	}
	if rb.head != 0 {
		t.Errorf("Expected head 0 after clear, got %d", rb.head)
	}
	if rb.tail != 0 {
		t.Errorf("Expected tail 0 after clear, got %d", rb.tail)
	}

	messages := rb.ReadAll()
	if len(messages) != 0 {
		t.Errorf("Expected 0 messages after clear, got %d", len(messages))
	}
}

func TestRingBuffer_Concurrent(t *testing.T) {
	rb := NewRingBuffer(1000)
	done := make(chan bool)

	// Writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			msg := &Message{Type: "stdout", Data: []byte{byte(i)}}
			rb.Write(msg)
		}
		done <- true
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < 100; i++ {
			rb.ReadAll()
		}
		done <- true
	}()

	// Clearer goroutine
	go func() {
		for i := 0; i < 10; i++ {
			rb.Clear()
		}
		done <- true
	}()

	// Wait for all goroutines
	<-done
	<-done
	<-done

	// Should not panic or deadlock
	rb.ReadAll()
}

func TestRingBuffer_MessageTypes(t *testing.T) {
	rb := NewRingBuffer(5)

	stdoutMsg := &Message{Type: "stdout", Data: []byte("output")}
	stderrMsg := &Message{Type: "stderr", Data: []byte("error")}
	exitMsg := &Message{Type: "exit", ExitCode: 1}

	rb.Write(stdoutMsg)
	rb.Write(stderrMsg)
	rb.Write(exitMsg)

	messages := rb.ReadAll()

	if len(messages) != 3 {
		t.Fatalf("Expected 3 messages, got %d", len(messages))
	}
	if messages[0].Type != "stdout" {
		t.Errorf("Expected first message type 'stdout', got '%s'", messages[0].Type)
	}
	if messages[1].Type != "stderr" {
		t.Errorf("Expected second message type 'stderr', got '%s'", messages[1].Type)
	}
	if messages[2].Type != "exit" {
		t.Errorf("Expected third message type 'exit', got '%s'", messages[2].Type)
	}
	if messages[2].ExitCode != 1 {
		t.Errorf("Expected exit code 1, got %d", messages[2].ExitCode)
	}
}
