package shellbridge

import (
	"encoding/binary"
	"errors"
)

// BinaryDataType represents the type of binary data in a frame
type BinaryDataType byte

const (
	DataTypeStdout BinaryDataType = 0x01
	DataTypeStderr BinaryDataType = 0x02
	DataTypeResize BinaryDataType = 0x03
	DataTypeClose  BinaryDataType = 0x04
)

// BinaryFrame represents a binary frame from shell-bridge
// Format: [Type:1][Length:4][Data:N]
type BinaryFrame struct {
	Type   BinaryDataType
	Length uint32
	Data   []byte
}

// ParseBinaryFrame parses a binary frame from shell-bridge
func ParseBinaryFrame(data []byte) (*BinaryFrame, error) {
	if len(data) < 5 { // 1 + 4 minimum
		return nil, errors.New("frame too short")
	}

	frame := &BinaryFrame{
		Type:   BinaryDataType(data[0]),
		Length: binary.BigEndian.Uint32(data[1:5]),
	}

	if frame.Length > 0 {
		if len(data) < 5+int(frame.Length) {
			return nil, errors.New("incomplete frame data")
		}
		frame.Data = data[5 : 5+frame.Length]
	}

	return frame, nil
}

// EncodeBinaryFrame encodes a binary frame for sending to shell-bridge
// Used by WebSocket handlers to send output to clients
func EncodeBinaryFrame(frameType BinaryDataType, data []byte) ([]byte, error) {
	buf := make([]byte, 5+len(data))

	// Type
	buf[0] = byte(frameType)

	// Length
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(data)))

	// Data
	copy(buf[5:], data)

	return buf, nil
}
