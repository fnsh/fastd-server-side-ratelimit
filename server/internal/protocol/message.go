package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// ProtocolVersion is the only supported wire version.
	ProtocolVersion uint8 = 1

	// MessageSize is the fixed on-wire size of the protocol frame.
	MessageSize = 256

	messageReservedSize = MessageSize - 201
)

var (
	errInvalidMessageSize = errors.New("protocol: invalid message size")
	errUnsupportedVersion = errors.New("protocol: unsupported message version")
)

// MachineInformation contains the fixed-size machine identity block.
type MachineInformation struct {
	Target       [32]byte
	Subtarget    [32]byte
	CPUCoreCount uint8
	Reserved     [63]byte
}

// Message is the fixed-size wire format exchanged between client and server.
type Message struct {
	Version            uint8
	SequenceNumber     uint32
	MachineInformation MachineInformation

	Load1        uint8
	Load5        uint8
	Load15       uint8
	LoadReserved uint8

	PktsSent uint64
	KBsSent  uint64
	PktsRecv uint64
	KBsRecv  uint64

	DownstreamCurrent    uint32
	DownstreamConfigured uint32
	DownstreamMin        uint32
	DownstreamMax        uint32

	UpstreamCurrent    uint32
	UpstreamConfigured uint32
	UpstreamMin        uint32
	UpstreamMax        uint32

	Reserved [messageReservedSize]byte
}

func (m Message) String() string {
	return fmt.Sprintf("Version=%d Seq=%d Target=%s Subtarget=%s CPUCoreCount=%d Load1=%d Load5=%d Load15=%d PktsSent=%d KBsSent=%d PktsRecv=%d KBsRecv=%d DownstreamCurrent=%d DownstreamConfigured=%d DownstreamMin=%d DownstreamMax=%d UpstreamCurrent=%d UpstreamConfigured=%d UpstreamMin=%d UpstreamMax=%d",
		m.Version, m.SequenceNumber, string(m.MachineInformation.Target[:]), string(m.MachineInformation.Subtarget[:]), m.MachineInformation.CPUCoreCount,
		m.Load1, m.Load5, m.Load15,
		m.PktsSent, m.KBsSent, m.PktsRecv, m.KBsRecv,
		m.DownstreamCurrent, m.DownstreamConfigured, m.DownstreamMin, m.DownstreamMax,
		m.UpstreamCurrent, m.UpstreamConfigured, m.UpstreamMin, m.UpstreamMax)
}

func (m Message) Clone() (Message, error) {
	data, err := m.MarshalBinary()
	if err != nil {
		return Message{}, fmt.Errorf("failed to marshal message for cloning: %v", err)
	}
	clonedMessage, err := MessageFromBytes(data)
	if err != nil {
		return Message{}, fmt.Errorf("failed to unmarshal message for cloning: %v", err)
	}
	return clonedMessage, nil
}

// MarshalBinary serializes the message into its fixed-size wire format.
func (m Message) MarshalBinary() ([]byte, error) {
	if m.Version == 0 {
		m.Version = ProtocolVersion
	}
	if m.Version != ProtocolVersion {
		return nil, fmt.Errorf("protocol: %w: %d", errUnsupportedVersion, m.Version)
	}

	buf := make([]byte, MessageSize)
	offset := 0

	buf[offset] = m.Version
	offset++
	binary.BigEndian.PutUint32(buf[offset:], m.SequenceNumber)
	offset += 4

	copy(buf[offset:], m.MachineInformation.Target[:])
	offset += len(m.MachineInformation.Target)
	copy(buf[offset:], m.MachineInformation.Subtarget[:])
	offset += len(m.MachineInformation.Subtarget)
	buf[offset] = m.MachineInformation.CPUCoreCount
	offset++
	copy(buf[offset:], m.MachineInformation.Reserved[:])
	offset += len(m.MachineInformation.Reserved)

	buf[offset] = m.Load1
	offset++
	buf[offset] = m.Load5
	offset++
	buf[offset] = m.Load15
	offset++
	buf[offset] = m.LoadReserved
	offset++

	binary.BigEndian.PutUint64(buf[offset:], m.PktsSent)
	offset += 8
	binary.BigEndian.PutUint64(buf[offset:], m.KBsSent)
	offset += 8
	binary.BigEndian.PutUint64(buf[offset:], m.PktsRecv)
	offset += 8
	binary.BigEndian.PutUint64(buf[offset:], m.KBsRecv)
	offset += 8

	binary.BigEndian.PutUint32(buf[offset:], m.DownstreamCurrent)
	offset += 4
	binary.BigEndian.PutUint32(buf[offset:], m.DownstreamConfigured)
	offset += 4
	binary.BigEndian.PutUint32(buf[offset:], m.DownstreamMin)
	offset += 4
	binary.BigEndian.PutUint32(buf[offset:], m.DownstreamMax)
	offset += 4

	binary.BigEndian.PutUint32(buf[offset:], m.UpstreamCurrent)
	offset += 4
	binary.BigEndian.PutUint32(buf[offset:], m.UpstreamConfigured)
	offset += 4
	binary.BigEndian.PutUint32(buf[offset:], m.UpstreamMin)
	offset += 4
	binary.BigEndian.PutUint32(buf[offset:], m.UpstreamMax)
	offset += 4

	copy(buf[offset:], m.Reserved[:])

	return buf, nil
}

// UnmarshalBinary decodes a fixed-size wire-format message.
func (m *Message) UnmarshalBinary(data []byte) error {
	if len(data) != MessageSize {
		return fmt.Errorf("protocol: %w: got %d want %d", errInvalidMessageSize, len(data), MessageSize)
	}

	offset := 0
	m.Version = data[offset]
	offset++
	if m.Version != ProtocolVersion {
		return fmt.Errorf("protocol: %w: %d", errUnsupportedVersion, m.Version)
	}

	m.SequenceNumber = binary.BigEndian.Uint32(data[offset:])
	offset += 4

	copy(m.MachineInformation.Target[:], data[offset:offset+len(m.MachineInformation.Target)])
	offset += len(m.MachineInformation.Target)
	copy(m.MachineInformation.Subtarget[:], data[offset:offset+len(m.MachineInformation.Subtarget)])
	offset += len(m.MachineInformation.Subtarget)
	m.MachineInformation.CPUCoreCount = data[offset]
	offset++
	copy(m.MachineInformation.Reserved[:], data[offset:offset+len(m.MachineInformation.Reserved)])
	offset += len(m.MachineInformation.Reserved)

	if offset+4 > len(data) {
		return fmt.Errorf("protocol: truncated")
	}
	m.Load1 = data[offset]
	offset++
	m.Load5 = data[offset]
	offset++
	m.Load15 = data[offset]
	offset++
	m.LoadReserved = data[offset]
	offset++

	m.PktsSent = binary.BigEndian.Uint64(data[offset:])
	offset += 8
	m.KBsSent = binary.BigEndian.Uint64(data[offset:])
	offset += 8
	m.PktsRecv = binary.BigEndian.Uint64(data[offset:])
	offset += 8
	m.KBsRecv = binary.BigEndian.Uint64(data[offset:])
	offset += 8

	m.DownstreamCurrent = binary.BigEndian.Uint32(data[offset:])
	offset += 4
	m.DownstreamConfigured = binary.BigEndian.Uint32(data[offset:])
	offset += 4
	m.DownstreamMin = binary.BigEndian.Uint32(data[offset:])
	offset += 4
	m.DownstreamMax = binary.BigEndian.Uint32(data[offset:])
	offset += 4

	m.UpstreamCurrent = binary.BigEndian.Uint32(data[offset:])
	offset += 4
	m.UpstreamConfigured = binary.BigEndian.Uint32(data[offset:])
	offset += 4
	m.UpstreamMin = binary.BigEndian.Uint32(data[offset:])
	offset += 4
	m.UpstreamMax = binary.BigEndian.Uint32(data[offset:])
	offset += 4

	copy(m.Reserved[:], data[offset:offset+len(m.Reserved)])

	return nil
}

// Bytes returns the serialized fixed-size wire format.
func (m Message) Bytes() ([]byte, error) {
	return m.MarshalBinary()
}

// MessageFromBytes parses a serialized message into a struct value.
func MessageFromBytes(data []byte) (Message, error) {
	var message Message
	if err := message.UnmarshalBinary(data); err != nil {
		return Message{}, err
	}
	return message, nil
}
