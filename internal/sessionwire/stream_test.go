package sessionwire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"testing"
)

func TestArbitraryBinaryPayloadRoundTrip(t *testing.T) {
	t.Parallel()

	payload := []byte{0x00, 0x01, 0x1b, '[', '2', 'J', 0x7f, 0x80, 0xfe, 0xff, '\n'}
	var stream bytes.Buffer
	writer := NewWriter(&stream, SupervisorToDaemon)
	if err := writer.Write(TypeTerminal, payload); err != nil {
		t.Fatal(err)
	}

	frame, err := NewReader(&stream, SupervisorToDaemon).ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if frame.Type != TypeTerminal || !bytes.Equal(frame.Payload, payload) {
		t.Fatalf("frame=%s %x, want terminal %x", frame.Type, frame.Payload, payload)
	}
}

func TestOversizedPayloadFailsBeforeAllocationOrWrite(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	err := NewWriter(&output, DaemonToClient).Write(TypeTerminal, make([]byte, int(MaxPayloadSize)+1))
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("writer error=%v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("oversized writer emitted %d bytes", output.Len())
	}

	var header [HeaderSize]byte
	header[0] = byte(TypeTerminal)
	binary.BigEndian.PutUint32(header[1:], MaxPayloadSize+1)
	_, err = NewReader(bytes.NewReader(header[:]), DaemonToClient).ReadFrame()
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("reader error=%v", err)
	}
}

func TestTruncatedHeaderAndPayloadFailClosed(t *testing.T) {
	t.Parallel()

	if _, err := NewReader(bytes.NewReader([]byte{byte(TypeTerminal), 0}), DaemonToClient).ReadFrame(); !errors.Is(err, ErrTruncatedFrame) {
		t.Fatalf("header error=%v", err)
	}

	var encoded bytes.Buffer
	header := []byte{byte(TypeTerminal), 0, 0, 0, 4}
	encoded.Write(header)
	encoded.Write([]byte{1, 2})
	if _, err := NewReader(&encoded, DaemonToClient).ReadFrame(); !errors.Is(err, ErrTruncatedFrame) {
		t.Fatalf("payload error=%v", err)
	}

	if _, err := NewReader(bytes.NewReader(nil), DaemonToClient).ReadFrame(); !errors.Is(err, io.EOF) {
		t.Fatalf("clean EOF error=%v", err)
	}
}

func TestWrongDirectionFailsForReadAndWrite(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	if err := NewWriter(&output, ClientToDaemon).Write(TypeTerminal, []byte("output")); !errors.Is(err, ErrWrongDirection) {
		t.Fatalf("writer error=%v", err)
	}

	encoded := []byte{byte(TypeTerminal), 0, 0, 0, 0}
	if _, err := NewReader(bytes.NewReader(encoded), ClientToDaemon).ReadFrame(); !errors.Is(err, ErrWrongDirection) {
		t.Fatalf("reader error=%v", err)
	}
}

func TestUnknownMandatoryFailsAndOptionalExtensionIsConsumable(t *testing.T) {
	t.Parallel()

	unknownMandatory := []byte{0x7f, 0, 0, 0, 0}
	if _, err := NewReader(bytes.NewReader(unknownMandatory), ClientToDaemon).ReadFrame(); !errors.Is(err, ErrUnknownMandatoryFrame) {
		t.Fatalf("mandatory error=%v", err)
	}

	var stream bytes.Buffer
	writer := NewWriter(&stream, ClientToDaemon)
	extensionPayload := []byte{0xde, 0xad, 0x00, 0xbe, 0xef}
	if err := writer.Write(Type(0x80), extensionPayload); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteControl(TypeHello, &Hello{Protocol: Protocol, Token: "operator-token"}); err != nil {
		t.Fatal(err)
	}

	reader := NewReader(&stream, ClientToDaemon)
	extension, err := reader.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if extension.Type != Type(0x80) || !bytes.Equal(extension.Payload, extensionPayload) {
		t.Fatalf("extension=%s %x", extension.Type, extension.Payload)
	}
	hello, err := reader.ReadFrame()
	if err != nil {
		t.Fatalf("frame after ignored extension: %v", err)
	}
	if hello.Type != TypeHello {
		t.Fatalf("frame after extension=%s, want hello", hello.Type)
	}
}

func TestWriterSerializesConcurrentFrames(t *testing.T) {
	t.Parallel()

	const frameCount = 64
	var stream bytes.Buffer
	writer := NewWriter(&stream, DaemonToClient)
	start := make(chan struct{})
	errs := make(chan error, frameCount)
	var wg sync.WaitGroup
	for i := 0; i < frameCount; i++ {
		wg.Add(1)
		go func(value byte) {
			defer wg.Done()
			<-start
			payload := bytes.Repeat([]byte{value}, 257)
			errs <- writer.Write(TypeTerminal, payload)
		}(byte(i + 1))
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	reader := NewReader(&stream, DaemonToClient)
	seen := make(map[byte]bool, frameCount)
	for i := 0; i < frameCount; i++ {
		frame, err := reader.ReadFrame()
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if len(frame.Payload) != 257 {
			t.Fatalf("frame %d payload length=%d", i, len(frame.Payload))
		}
		value := frame.Payload[0]
		if seen[value] {
			t.Fatalf("duplicate payload marker %d", value)
		}
		seen[value] = true
		if !bytes.Equal(frame.Payload, bytes.Repeat([]byte{value}, len(frame.Payload))) {
			t.Fatalf("frame %d was interleaved", i)
		}
	}
	if _, err := reader.ReadFrame(); !errors.Is(err, io.EOF) {
		t.Fatalf("final read error=%v", err)
	}
}
