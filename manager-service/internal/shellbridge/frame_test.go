package shellbridge

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestParseBinaryFrame(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		wantFrame *BinaryFrame
		wantError bool
	}{
		{
			name: "Valid stdout frame",
			data: []byte{0x01, 0x00, 0x00, 0x00, 0x05, 'h', 'e', 'l', 'l', 'o'},
			wantFrame: &BinaryFrame{
				Type:   DataTypeStdout,
				Length: 5,
				Data:   []byte("hello"),
			},
			wantError: false,
		},
		{
			name: "Valid stderr frame",
			data: []byte{0x02, 0x00, 0x00, 0x00, 0x06, 'e', 'r', 'r', 'o', 'r', '!'},
			wantFrame: &BinaryFrame{
				Type:   DataTypeStderr,
				Length: 6,
				Data:   []byte("error!"),
			},
			wantError: false,
		},
		{
			name: "Valid resize frame",
			data: []byte{0x03, 0x00, 0x00, 0x00, 0x04, 80, 0, 24, 0},
			wantFrame: &BinaryFrame{
				Type:   DataTypeResize,
				Length: 4,
				Data:   []byte{80, 0, 24, 0},
			},
			wantError: false,
		},
		{
			name: "Valid close frame with empty data",
			data: []byte{0x04, 0x00, 0x00, 0x00, 0x00},
			wantFrame: &BinaryFrame{
				Type:   DataTypeClose,
				Length: 0,
				Data:   []byte{},
			},
			wantError: false,
		},
		{
			name:      "Empty data",
			data:      []byte{},
			wantError: true,
		},
		{
			name:      "Incomplete header",
			data:      []byte{0x01, 0x00, 0x00},
			wantError: true,
		},
		{
			name:      "Data shorter than length",
			data:      []byte{0x01, 0x00, 0x00, 0x00, 0x05, 'h', 'i'},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frame, err := ParseBinaryFrame(tt.data)
			if (err != nil) != tt.wantError {
				t.Errorf("ParseBinaryFrame() error = %v, wantError %v", err, tt.wantError)
				return
			}

			if !tt.wantError {
				if frame.Type != tt.wantFrame.Type {
					t.Errorf("Type = %v, want %v", frame.Type, tt.wantFrame.Type)
				}
				if frame.Length != tt.wantFrame.Length {
					t.Errorf("Length = %v, want %v", frame.Length, tt.wantFrame.Length)
				}
				if !bytes.Equal(frame.Data, tt.wantFrame.Data) {
					t.Errorf("Data = %v, want %v", frame.Data, tt.wantFrame.Data)
				}
			}
		})
	}
}

func TestEncodeBinaryFrame(t *testing.T) {
	tests := []struct {
		name      string
		frameType BinaryDataType
		data      []byte
		wantError bool
	}{
		{
			name:      "Encode stdout frame",
			frameType: DataTypeStdout,
			data:      []byte("hello"),
			wantError: false,
		},
		{
			name:      "Encode stderr frame",
			frameType: DataTypeStderr,
			data:      []byte("error"),
			wantError: false,
		},
		{
			name:      "Encode resize frame",
			frameType: DataTypeResize,
			data:      []byte{80, 0, 24, 0},
			wantError: false,
		},
		{
			name:      "Encode close frame with empty data",
			frameType: DataTypeClose,
			data:      []byte{},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := EncodeBinaryFrame(tt.frameType, tt.data)
			if (err != nil) != tt.wantError {
				t.Errorf("EncodeBinaryFrame() error = %v, wantError %v", err, tt.wantError)
				return
			}

			// Verify format
			if len(encoded) < 5 {
				t.Fatalf("Encoded data too short: %d bytes", len(encoded))
			}

			// Check type
			if encoded[0] != byte(tt.frameType) {
				t.Errorf("Type byte = %v, want %v", encoded[0], tt.frameType)
			}

			// Check length
			length := binary.BigEndian.Uint32(encoded[1:5])
			if length != uint32(len(tt.data)) {
				t.Errorf("Length = %v, want %v", length, len(tt.data))
			}

			// Check data
			if !bytes.Equal(encoded[5:], tt.data) {
				t.Errorf("Data = %v, want %v", encoded[5:], tt.data)
			}
		})
	}
}

func TestFrameRoundTrip(t *testing.T) {
	original := &BinaryFrame{
		Type:   DataTypeStdout,
		Length: 43,
		Data:   []byte("The quick brown fox jumps over the lazy dog"),
	}

	// Encode
	encoded, err := EncodeBinaryFrame(original.Type, original.Data)
	if err != nil {
		t.Fatalf("EncodeBinaryFrame() error = %v", err)
	}

	// Parse
	decoded, err := ParseBinaryFrame(encoded)
	if err != nil {
		t.Fatalf("ParseBinaryFrame() error = %v", err)
	}

	// Verify
	if decoded.Type != original.Type {
		t.Errorf("Type = %v, want %v", decoded.Type, original.Type)
	}
	if decoded.Length != original.Length {
		t.Errorf("Length = %v, want %v", decoded.Length, original.Length)
	}
	if !bytes.Equal(decoded.Data, original.Data) {
		t.Errorf("Data = %v, want %v", decoded.Data, original.Data)
	}
}

func TestBinaryDataTypeValues(t *testing.T) {
	tests := []struct {
		name     string
		dataType BinaryDataType
		want     byte
	}{
		{"Data type stdout", DataTypeStdout, 0x01},
		{"Data type stderr", DataTypeStderr, 0x02},
		{"Data type resize", DataTypeResize, 0x03},
		{"Data type close", DataTypeClose, 0x04},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := byte(tt.dataType); got != tt.want {
				t.Errorf("BinaryDataType = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFrameSizeLimit(t *testing.T) {
	// Create a frame larger than 10MB
	largeData := make([]byte, 11*1024*1024) // 11MB

	frame := &BinaryFrame{
		Type:   DataTypeStdout,
		Length: uint32(len(largeData)),
		Data:   largeData,
	}

	_, err := EncodeBinaryFrame(frame.Type, frame.Data)

	if err == nil {
		t.Error("Expected error for frame > 10MB, got nil")
	}
}

func TestFrameOverflowProtection(t *testing.T) {
	// Create malicious frame with Length that overflows when converted to int
	// Max uint32 value (2^32 - 1) would be problematic
	buf := make([]byte, 5)
	buf[0] = byte(DataTypeStdout)
	// Max uint32 value - this should be rejected before int conversion
	buf[1] = 0xFF
	buf[2] = 0xFF
	buf[3] = 0xFF
	buf[4] = 0xFF

	_, err := ParseBinaryFrame(buf)
	if err == nil {
		t.Error("Expected error for oversized frame, got nil")
	}
}

func TestFrameOverflowProtectionNearLimit(t *testing.T) {
	// Test with value slightly larger than maxFrameSize (10MB)
	maxSize := 10 * 1024 * 1024
	_ = maxSize + 1 // Used for clarity in test below

	buf := make([]byte, 5)
	buf[0] = byte(DataTypeStdout)
	// Put length that's 1 byte larger than max: 0x00A00001 (10MB + 1)
	buf[1] = 0x00
	buf[2] = 0xA0
	buf[3] = 0x00
	buf[4] = 0x01

	_, err := ParseBinaryFrame(buf)
	if err == nil {
		t.Error("Expected error for frame exceeding max size, got nil")
	}
}

func TestValidFrameUnderLimit(t *testing.T) {
	// Test with valid frame at exactly 10MB (should work)
	tenMB := 10 * 1024 * 1024
	buf := make([]byte, 5+tenMB)
	buf[0] = byte(DataTypeStdout)
	// Put length exactly 10MB in big endian: 0x00A00000
	buf[1] = 0x00
	buf[2] = 0xA0
	buf[3] = 0x00
	buf[4] = 0x00
	// Fill rest with valid data
	for i := 5; i < len(buf); i++ {
		buf[i] = 0x42 // 'B'
	}

	frame, err := ParseBinaryFrame(buf)
	if err != nil {
		t.Errorf("Expected success for frame at max size, got error: %v", err)
	}
	if frame != nil {
		if frame.Type != DataTypeStdout {
			t.Errorf("Expected Type DataTypeStdout, got %v", frame.Type)
		}
		if frame.Length != uint32(tenMB) {
			t.Errorf("Expected Length %d, got %d", tenMB, frame.Length)
		}
		if len(frame.Data) != tenMB {
			t.Errorf("Expected data length %d, got %d", tenMB, len(frame.Data))
		}
	}
}

func TestValidSmallFrameParses(t *testing.T) {
	// Create a valid frame with 5 bytes of data
	buf := []byte{
		0x01,       // Type: DataTypeStdout
		0x00, 0x00, 0x00, 0x05, // Length: 5
		0x48, 0x65, 0x6C, 0x6C, 0x6F, // Data: 'H', 'e', 'l', 'l', 'o'
	}

	frame, err := ParseBinaryFrame(buf)
	if err != nil {
		t.Fatalf("Expected success, got error: %v", err)
	}
	if frame.Type != DataTypeStdout {
		t.Errorf("Expected Type DataTypeStdout, got 0x%02x", frame.Type)
	}
	if frame.Length != 5 {
		t.Errorf("Expected Length 5, got %d", frame.Length)
	}
	if len(frame.Data) != 5 {
		t.Errorf("Expected data length 5, got %d", len(frame.Data))
	}
	if string(frame.Data) != "Hello" {
		t.Errorf("Expected data 'Hello', got '%s'", string(frame.Data))
	}
}

func TestMaxFrameSizeConstant(t *testing.T) {
	// Verify MaxFrameSize is exported and has the expected value
	if MaxFrameSize != 10*1024*1024 {
		t.Errorf("Expected MaxFrameSize to be 10MB, got %d", MaxFrameSize)
	}
}
