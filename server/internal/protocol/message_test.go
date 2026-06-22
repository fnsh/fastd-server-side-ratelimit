package protocol

import (
	"bytes"
	"testing"
)

func TestMessageMarshalUnmarshalRoundTrip(t *testing.T) {
	var original Message
	original.Version = ProtocolVersion
	original.SequenceNumber = 42
	copy(original.MachineInformation.Target[:], []byte("target"))
	copy(original.MachineInformation.Subtarget[:], []byte("subtarget"))
	copy(original.MachineInformation.Reserved[:], []byte("machine-reserved"))
	original.Load1 = 1
	original.Load5 = 2
	original.Load15 = 3
	original.LoadReserved = 4

	original.PktsSent = 11
	original.KBsSent = 22
	original.PktsRecv = 33
	original.KBsRecv = 44

	original.DownstreamCurrent = 100
	original.DownstreamConfigured = 200
	original.DownstreamMin = 300
	original.DownstreamMax = 400

	original.UpstreamCurrent = 500
	original.UpstreamConfigured = 600
	original.UpstreamMin = 700
	original.UpstreamMax = 800
	copy(original.Reserved[:], []byte("trailing-reserved"))

	data, err := original.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if len(data) != MessageSize {
		t.Fatalf("unexpected serialized size: got %d want %d", len(data), MessageSize)
	}

	var decoded Message
	if err := decoded.UnmarshalBinary(data); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.Version != original.Version {
		t.Fatalf("version mismatch: got %d want %d", decoded.Version, original.Version)
	}
	if decoded.SequenceNumber != original.SequenceNumber {
		t.Fatalf("sequence mismatch: got %d want %d", decoded.SequenceNumber, original.SequenceNumber)
	}
	if !bytes.Equal(decoded.MachineInformation.Target[:], original.MachineInformation.Target[:]) {
		t.Fatalf("target mismatch")
	}
	if !bytes.Equal(decoded.MachineInformation.Subtarget[:], original.MachineInformation.Subtarget[:]) {
		t.Fatalf("subtarget mismatch")
	}
	if !bytes.Equal(decoded.MachineInformation.Reserved[:], original.MachineInformation.Reserved[:]) {
		t.Fatalf("machine reserved mismatch")
	}
	if decoded.Load1 != original.Load1 || decoded.Load5 != original.Load5 || decoded.Load15 != original.Load15 || decoded.LoadReserved != original.LoadReserved {
		t.Fatalf("load average mismatch: got %d,%d,%d,%d want %d,%d,%d,%d", decoded.Load1, decoded.Load5, decoded.Load15, decoded.LoadReserved, original.Load1, original.Load5, original.Load15, original.LoadReserved)
	}
	if decoded.PktsSent != original.PktsSent || decoded.KBsSent != original.KBsSent || decoded.PktsRecv != original.PktsRecv || decoded.KBsRecv != original.KBsRecv {
		t.Fatalf("packet counters mismatch: got %v want %v", []uint64{decoded.PktsSent, decoded.KBsSent, decoded.PktsRecv, decoded.KBsRecv}, []uint64{original.PktsSent, original.KBsSent, original.PktsRecv, original.KBsRecv})
	}
	if decoded.DownstreamCurrent != original.DownstreamCurrent || decoded.DownstreamConfigured != original.DownstreamConfigured || decoded.DownstreamMin != original.DownstreamMin || decoded.DownstreamMax != original.DownstreamMax {
		t.Fatalf("downstream rates mismatch: got %v want %v", []uint32{decoded.DownstreamCurrent, decoded.DownstreamConfigured, decoded.DownstreamMin, decoded.DownstreamMax}, []uint32{original.DownstreamCurrent, original.DownstreamConfigured, original.DownstreamMin, original.DownstreamMax})
	}
	if decoded.UpstreamCurrent != original.UpstreamCurrent || decoded.UpstreamConfigured != original.UpstreamConfigured || decoded.UpstreamMin != original.UpstreamMin || decoded.UpstreamMax != original.UpstreamMax {
		t.Fatalf("upstream rates mismatch: got %v want %v", []uint32{decoded.UpstreamCurrent, decoded.UpstreamConfigured, decoded.UpstreamMin, decoded.UpstreamMax}, []uint32{original.UpstreamCurrent, original.UpstreamConfigured, original.UpstreamMin, original.UpstreamMax})
	}
	if !bytes.Equal(decoded.Reserved[:], original.Reserved[:]) {
		t.Fatalf("trailing reserved mismatch")
	}
}

func TestMessageRejectsWrongSize(t *testing.T) {
	var message Message
	if err := message.UnmarshalBinary(make([]byte, MessageSize-1)); err == nil {
		t.Fatal("expected size error")
	}
}

func TestMessageRejectsWrongVersion(t *testing.T) {
	data := make([]byte, MessageSize)
	data[0] = 2

	var message Message
	if err := message.UnmarshalBinary(data); err == nil {
		t.Fatal("expected version error")
	}
}
