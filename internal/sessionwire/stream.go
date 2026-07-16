package sessionwire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

type Reader struct {
	reader    io.Reader
	direction Direction
	limit     uint32
}

func NewReader(reader io.Reader, direction Direction) *Reader {
	return NewReaderWithLimit(reader, direction, MaxPayloadSize)
}

func NewReaderWithLimit(reader io.Reader, direction Direction, limit uint32) *Reader {
	if limit == 0 || limit > MaxPayloadSize {
		limit = MaxPayloadSize
	}
	return &Reader{reader: reader, direction: direction, limit: limit}
}

func (r *Reader) ReadFrame() (Frame, error) {
	if r == nil || r.reader == nil {
		return Frame{}, errors.New("session wire reader is nil")
	}
	var header [HeaderSize]byte
	n, err := io.ReadFull(r.reader, header[:])
	if err != nil {
		if errors.Is(err, io.EOF) && n == 0 {
			return Frame{}, io.EOF
		}
		return Frame{}, fmt.Errorf("%w: header: %v", ErrTruncatedFrame, err)
	}

	frameType := Type(header[0])
	if err := ValidateDirection(r.direction, frameType); err != nil {
		return Frame{}, err
	}
	length := binary.BigEndian.Uint32(header[1:])
	if length > r.limit {
		return Frame{}, fmt.Errorf("%w: type=%s size=%d limit=%d", ErrPayloadTooLarge, frameType, length, r.limit)
	}

	payload := make([]byte, int(length))
	if length > 0 {
		if _, err := io.ReadFull(r.reader, payload); err != nil {
			return Frame{}, fmt.Errorf("%w: type=%s payload: %v", ErrTruncatedFrame, frameType, err)
		}
	}
	frame := Frame{Type: frameType, Payload: payload}
	if err := validateFrame(r.direction, frame, r.limit); err != nil {
		return Frame{}, err
	}
	return frame, nil
}

type Writer struct {
	mu        sync.Mutex
	writer    io.Writer
	direction Direction
	limit     uint32
}

func NewWriter(writer io.Writer, direction Direction) *Writer {
	return NewWriterWithLimit(writer, direction, MaxPayloadSize)
}

func NewWriterWithLimit(writer io.Writer, direction Direction, limit uint32) *Writer {
	if limit == 0 || limit > MaxPayloadSize {
		limit = MaxPayloadSize
	}
	return &Writer{writer: writer, direction: direction, limit: limit}
}

func (w *Writer) Write(frameType Type, payload []byte) error {
	return w.WriteFrame(Frame{Type: frameType, Payload: payload})
}

func (w *Writer) WriteControl(frameType Type, control Control) error {
	payload, err := MarshalControl(control)
	if err != nil {
		return err
	}
	return w.Write(frameType, payload)
}

func (w *Writer) WriteFrame(frame Frame) error {
	if w == nil || w.writer == nil {
		return errors.New("session wire writer is nil")
	}
	if err := validateFrame(w.direction, frame, w.limit); err != nil {
		return err
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	var header [HeaderSize]byte
	header[0] = byte(frame.Type)
	binary.BigEndian.PutUint32(header[1:], uint32(len(frame.Payload)))
	if err := writeAll(w.writer, header[:]); err != nil {
		return fmt.Errorf("write session wire header: %w", err)
	}
	if err := writeAll(w.writer, frame.Payload); err != nil {
		return fmt.Errorf("write session wire payload: %w", err)
	}
	return nil
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
