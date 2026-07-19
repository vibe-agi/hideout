package workspaceattach

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"syscall"
)

const (
	portalHeaderBytes = 32
	portalVersion     = 1

	portalKindRequest  = 1
	portalKindResponse = 2
	portalKindTerminal = 3
	portalKindEvent    = 4

	portalOpHello     = 1
	portalOpCancel    = 2
	portalOpEcho      = 3
	portalOpOpen      = 10
	portalOpClose     = 11
	portalOpLock      = 12
	portalOpUnlock    = 13
	portalOpStat      = 20
	portalOpReadDir   = 21
	portalOpRead      = 22
	portalOpWrite     = 23
	portalOpFsync     = 24
	portalOpMkdir     = 25
	portalOpRemove    = 26
	portalOpRename    = 27
	portalOpSymlink   = 28
	portalOpReadlink  = 29
	portalOpChmod     = 30
	portalOpChtimes   = 31
	portalOpTruncate  = 32
	portalOpCheckLock = 33
	portalOpFsyncPath = 34
	portalOpNotify    = 40

	portalStatusOK          int32 = 0
	portalStatusProtocol    int32 = -1
	portalStatusAuth        int32 = -2
	portalStatusExpired     int32 = -3
	portalStatusRevoked     int32 = -4
	portalStatusCancelled   int32 = -5
	portalStatusOverloaded  int32 = -6
	portalStatusRootChanged int32 = -7
	portalStatusBadHandle   int32 = -8
	portalStatusNotifyLost  int32 = -9

	// Filesystem statuses are protocol values, not host syscall numbers. The
	// portal server runs on Darwin while the client normally runs on Linux, and
	// those platforms do not share a stable numeric errno ABI.
	portalStatusFSNotFound    int32 = 1001
	portalStatusFSPermission  int32 = 1002
	portalStatusFSExists      int32 = 1003
	portalStatusFSNotDir      int32 = 1004
	portalStatusFSIsDir       int32 = 1005
	portalStatusFSNotEmpty    int32 = 1006
	portalStatusFSReadOnly    int32 = 1007
	portalStatusFSNoSpace     int32 = 1008
	portalStatusFSInvalid     int32 = 1009
	portalStatusFSOverflow    int32 = 1010
	portalStatusFSUnsupported int32 = 1011
	portalStatusFSLoop        int32 = 1012
	portalStatusFSBusy        int32 = 1013
	portalStatusFSBadFD       int32 = 1014
	portalStatusFSInterrupted int32 = 1015
	portalStatusFSWouldBlock  int32 = 1016
	portalStatusFSIO          int32 = 1099
)

var portalMagic = [4]byte{'H', 'W', 'P', '1'}

const (
	portalOpenReadOnly uint32 = iota
	portalOpenWriteOnly
	portalOpenReadWrite
)

const (
	portalOpenAppend uint32 = 1 << (iota + 2)
	portalOpenCreate
	portalOpenExclusive
	portalOpenTruncate
	portalOpenSync
	portalOpenNoFollow
)

type portalFrame struct {
	Kind      uint8
	Opcode    uint16
	Flags     uint32
	RequestID uint64
	Status    int32
	Payload   []byte
}

func writePortalFrame(writer io.Writer, frame portalFrame, maxPayload int) error {
	if len(frame.Payload) > maxPayload {
		return ErrPortalFrameTooLarge
	}
	if frame.Kind == 0 {
		frame.Kind = portalKindRequest
	}
	buffer := make([]byte, portalHeaderBytes+len(frame.Payload))
	copy(buffer[:4], portalMagic[:])
	buffer[4] = portalVersion
	buffer[5] = frame.Kind
	binary.BigEndian.PutUint16(buffer[6:8], frame.Opcode)
	binary.BigEndian.PutUint32(buffer[8:12], frame.Flags)
	binary.BigEndian.PutUint64(buffer[12:20], frame.RequestID)
	binary.BigEndian.PutUint32(buffer[20:24], uint32(len(frame.Payload)))
	binary.BigEndian.PutUint32(buffer[24:28], uint32(frame.Status))
	copy(buffer[portalHeaderBytes:], frame.Payload)
	_, err := writer.Write(buffer)
	return err
}

func readPortalFrame(reader io.Reader, maxPayload int) (portalFrame, error) {
	header := make([]byte, portalHeaderBytes)
	if _, err := io.ReadFull(reader, header); err != nil {
		return portalFrame{}, err
	}
	if !bytes.Equal(header[:4], portalMagic[:]) || header[4] != portalVersion {
		return portalFrame{}, ErrPortalProtocol
	}
	payloadSize := binary.BigEndian.Uint32(header[20:24])
	if payloadSize > uint32(maxPayload) {
		return portalFrame{}, ErrPortalFrameTooLarge
	}
	frame := portalFrame{
		Kind: header[5], Opcode: binary.BigEndian.Uint16(header[6:8]),
		Flags:     binary.BigEndian.Uint32(header[8:12]),
		RequestID: binary.BigEndian.Uint64(header[12:20]),
		Status:    int32(binary.BigEndian.Uint32(header[24:28])),
		Payload:   make([]byte, int(payloadSize)),
	}
	if _, err := io.ReadFull(reader, frame.Payload); err != nil {
		return portalFrame{}, err
	}
	return frame, nil
}

type portalEncoder struct{ bytes.Buffer }

func (encoder *portalEncoder) uint32(value uint32) {
	_ = binary.Write(&encoder.Buffer, binary.BigEndian, value)
}
func (encoder *portalEncoder) uint64(value uint64) {
	_ = binary.Write(&encoder.Buffer, binary.BigEndian, value)
}
func (encoder *portalEncoder) int64(value int64) {
	_ = binary.Write(&encoder.Buffer, binary.BigEndian, value)
}
func (encoder *portalEncoder) boolean(value bool) {
	if value {
		encoder.Buffer.WriteByte(1)
	} else {
		encoder.Buffer.WriteByte(0)
	}
}
func (encoder *portalEncoder) bytes(value []byte) {
	encoder.uint32(uint32(len(value)))
	encoder.Buffer.Write(value)
}
func (encoder *portalEncoder) string(value string) { encoder.bytes([]byte(value)) }

type portalDecoder struct {
	reader *bytes.Reader
}

func newPortalDecoder(payload []byte) *portalDecoder {
	return &portalDecoder{reader: bytes.NewReader(payload)}
}

func (decoder *portalDecoder) uint32() (uint32, error) {
	var value uint32
	err := binary.Read(decoder.reader, binary.BigEndian, &value)
	return value, err
}
func (decoder *portalDecoder) uint64() (uint64, error) {
	var value uint64
	err := binary.Read(decoder.reader, binary.BigEndian, &value)
	return value, err
}
func (decoder *portalDecoder) int64() (int64, error) {
	var value int64
	err := binary.Read(decoder.reader, binary.BigEndian, &value)
	return value, err
}
func (decoder *portalDecoder) boolean() (bool, error) {
	value, err := decoder.reader.ReadByte()
	if err != nil {
		return false, err
	}
	if value > 1 {
		return false, ErrPortalProtocol
	}
	return value == 1, nil
}
func (decoder *portalDecoder) bytes(max int) ([]byte, error) {
	size, err := decoder.uint32()
	if err != nil {
		return nil, err
	}
	if size > uint32(max) || int64(size) > int64(decoder.reader.Len()) {
		return nil, ErrPortalProtocol
	}
	value := make([]byte, int(size))
	_, err = io.ReadFull(decoder.reader, value)
	return value, err
}
func (decoder *portalDecoder) string(max int) (string, error) {
	value, err := decoder.bytes(max)
	return string(value), err
}
func (decoder *portalDecoder) done() error {
	if decoder.reader.Len() != 0 {
		return ErrPortalProtocol
	}
	return nil
}

func portalStatusForError(err error) int32 {
	if err == nil {
		return portalStatusOK
	}
	switch {
	case errors.Is(err, ErrPortalProtocol):
		return portalStatusProtocol
	case errors.Is(err, ErrPortalAuthentication):
		return portalStatusAuth
	case errors.Is(err, ErrPortalCredentialExpired):
		return portalStatusExpired
	case errors.Is(err, ErrPortalCredentialRevoked):
		return portalStatusRevoked
	case errors.Is(err, contextCanceledError):
		return portalStatusCancelled
	case errors.Is(err, ErrPortalOverloaded):
		return portalStatusOverloaded
	case errors.Is(err, ErrPortalRootReplaced):
		return portalStatusRootChanged
	case errors.Is(err, ErrPortalHandleNotFound):
		return portalStatusBadHandle
	case errors.Is(err, ErrPortalNotificationLost):
		return portalStatusNotifyLost
	}
	switch {
	case errors.Is(err, syscall.ENOENT):
		return portalStatusFSNotFound
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return portalStatusFSPermission
	case errors.Is(err, syscall.EEXIST):
		return portalStatusFSExists
	case errors.Is(err, syscall.ENOTDIR):
		return portalStatusFSNotDir
	case errors.Is(err, syscall.EISDIR):
		return portalStatusFSIsDir
	case errors.Is(err, syscall.ENOTEMPTY):
		return portalStatusFSNotEmpty
	case errors.Is(err, syscall.EROFS):
		return portalStatusFSReadOnly
	case errors.Is(err, syscall.ENOSPC):
		return portalStatusFSNoSpace
	case errors.Is(err, syscall.EINVAL):
		return portalStatusFSInvalid
	case errors.Is(err, syscall.EOVERFLOW):
		return portalStatusFSOverflow
	case errors.Is(err, syscall.EOPNOTSUPP):
		return portalStatusFSUnsupported
	case errors.Is(err, syscall.ELOOP):
		return portalStatusFSLoop
	case errors.Is(err, syscall.EBUSY):
		return portalStatusFSBusy
	case errors.Is(err, syscall.EBADF):
		return portalStatusFSBadFD
	case errors.Is(err, syscall.EINTR):
		return portalStatusFSInterrupted
	case errors.Is(err, syscall.EWOULDBLOCK):
		return portalStatusFSWouldBlock
	}
	return portalStatusFSIO
}

var contextCanceledError = errors.New("portal request cancelled")

func portalErrorForStatus(status int32) error {
	if status == 0 {
		return nil
	}
	switch status {
	case portalStatusProtocol:
		return ErrPortalProtocol
	case portalStatusAuth:
		return ErrPortalAuthentication
	case portalStatusExpired:
		return ErrPortalCredentialExpired
	case portalStatusRevoked:
		return ErrPortalCredentialRevoked
	case portalStatusCancelled:
		return contextCanceledError
	case portalStatusOverloaded:
		return ErrPortalOverloaded
	case portalStatusRootChanged:
		return ErrPortalRootReplaced
	case portalStatusBadHandle:
		return ErrPortalHandleNotFound
	case portalStatusNotifyLost:
		return ErrPortalNotificationLost
	case portalStatusFSNotFound:
		return syscall.ENOENT
	case portalStatusFSPermission:
		return syscall.EACCES
	case portalStatusFSExists:
		return syscall.EEXIST
	case portalStatusFSNotDir:
		return syscall.ENOTDIR
	case portalStatusFSIsDir:
		return syscall.EISDIR
	case portalStatusFSNotEmpty:
		return syscall.ENOTEMPTY
	case portalStatusFSReadOnly:
		return syscall.EROFS
	case portalStatusFSNoSpace:
		return syscall.ENOSPC
	case portalStatusFSInvalid:
		return syscall.EINVAL
	case portalStatusFSOverflow:
		return syscall.EOVERFLOW
	case portalStatusFSUnsupported:
		return syscall.EOPNOTSUPP
	case portalStatusFSLoop:
		return syscall.ELOOP
	case portalStatusFSBusy:
		return syscall.EBUSY
	case portalStatusFSBadFD:
		return syscall.EBADF
	case portalStatusFSInterrupted:
		return syscall.EINTR
	case portalStatusFSWouldBlock:
		return syscall.EWOULDBLOCK
	case portalStatusFSIO:
		return syscall.EIO
	}
	return fmt.Errorf("workspace portal status %d", status)
}
