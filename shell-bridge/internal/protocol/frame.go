package protocol

import (
	"encoding/binary"
	"errors"
	"io"
)

const (
	DataTypeStdout = 0x01
	DataTypeStderr = 0x02
	DataTypeResize = 0x03
	DataTypeClose  = 0x04

	maxFrameSize = 10 * 1024 * 1024 // 10MB
)

type Frame struct {
	Type   byte
	Length uint32
	Data   []byte
}

func ParseFrame(r io.Reader) (*Frame, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}

	frame := &Frame{
		Type:   header[0],
		Length: binary.BigEndian.Uint32(header[1:5]),
	}

	if frame.Length > maxFrameSize {
		return nil, errors.New("frame too large")
	}

	if frame.Length > 0 {
		frame.Data = make([]byte, frame.Length)
		if _, err := io.ReadFull(r, frame.Data); err != nil {
			return nil, err
		}
	}

	return frame, nil
}

func (f *Frame) Bytes() []byte {
	buf := make([]byte, 5+f.Length)
	buf[0] = f.Type
	binary.BigEndian.PutUint32(buf[1:5], f.Length)
	if f.Length > 0 {
		copy(buf[5:], f.Data)
	}
	return buf
}
