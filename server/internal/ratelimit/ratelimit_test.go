package ratelimit

import (
	"reflect"
	"testing"
	"time"

	"fastd-server-side-ratelimit/internal/config"
	"fastd-server-side-ratelimit/internal/protocol"
)

func setUintField(t *testing.T, v any, field string, value uint32) {
	t.Helper()

	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.Elem().Kind() != reflect.Struct {
		t.Fatalf("expected pointer to struct, got %T", v)
	}

	fv := rv.Elem().FieldByName(field)
	if !fv.IsValid() {
		t.Fatalf("field %q not found on %T", field, v)
	}
	if !fv.CanSet() {
		t.Fatalf("field %q on %T is not settable", field, v)
	}
	if fv.Kind() < reflect.Uint || fv.Kind() > reflect.Uint64 {
		t.Fatalf("field %q on %T is not an unsigned integer field", field, v)
	}
	fv.SetUint(uint64(value))
}

func setStringLikeField(t *testing.T, v reflect.Value, value string) {
	t.Helper()

	switch v.Kind() {
	case reflect.String:
		v.SetString(value)
	case reflect.Slice:
		if v.Type().Elem().Kind() != reflect.Uint8 {
			t.Fatalf("expected []byte-compatible slice, got %s", v.Type())
		}
		v.SetBytes([]byte(value))
	case reflect.Array:
		if v.Type().Elem().Kind() != reflect.Uint8 {
			t.Fatalf("expected [N]byte-compatible array, got %s", v.Type())
		}
		bytes := []byte(value)
		for i := 0; i < v.Len(); i++ {
			var b byte
			if i < len(bytes) {
				b = bytes[i]
			}
			v.Index(i).SetUint(uint64(b))
		}
	default:
		t.Fatalf("unsupported string-like field kind %s", v.Kind())
	}
}

func setMachineInformationField(t *testing.T, msg *protocol.Message, field, value string) {
	t.Helper()

	rv := reflect.ValueOf(msg).Elem()
	mf := rv.FieldByName("MachineInformation")
	if !mf.IsValid() {
		t.Fatalf("protocol.Message is missing MachineInformation field")
	}

	switch mf.Kind() {
	case reflect.Pointer:
		if mf.IsNil() {
			mf.Set(reflect.New(mf.Type().Elem()))
		}
		fv := mf.Elem().FieldByName(field)
		if !fv.IsValid() {
			t.Fatalf("protocol.Message.MachineInformation is missing field %q", field)
		}
		setStringLikeField(t, fv, value)
	case reflect.Struct:
		fv := mf.FieldByName(field)
		if !fv.IsValid() {
			t.Fatalf("protocol.Message.MachineInformation is missing field %q", field)
		}
		setStringLikeField(t, fv, value)
	default:
		t.Fatalf("unsupported MachineInformation kind %s", mf.Kind())
	}
}

func newTestMessage(t *testing.T, sequence uint32, target, subtarget string, limits ...uint32) protocol.Message {
	t.Helper()

	msg := protocol.Message{}
	setUintField(t, &msg, "SequenceNumber", sequence)
	setMachineInformationField(t, &msg, "Target", target)
	setMachineInformationField(t, &msg, "Subtarget", subtarget)

	fields := []string{"DownstreamMin", "UpstreamMin", "DownstreamMax", "UpstreamMax"}
	for i, field := range fields {
		if i >= len(limits) {
			break
		}
		setUintField(t, &msg, field, limits[i])
	}

	return msg
}

func getUintField(t *testing.T, v any, field string) uint32 {
	t.Helper()

	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		t.Fatalf("expected struct or pointer to struct, got %T", v)
	}

	fv := rv.FieldByName(field)
	if !fv.IsValid() {
		t.Fatalf("field %q not found on %T", field, v)
	}
	if fv.Kind() < reflect.Uint || fv.Kind() > reflect.Uint64 {
		t.Fatalf("field %q on %T is not an unsigned integer field", field, v)
	}
	return uint32(fv.Uint())
}

func TestMatchTargetLimitPrefersExactSubtarget(t *testing.T) {
	state := RateLimiterInterfaceState{}
	limits := []config.TargetRateLimit{
		{Target: "foo", Subtarget: "bar", InitialDownstreamRate: 11, InitialUpstreamRate: 22},
		{Target: "foo", Subtarget: "", InitialDownstreamRate: 33, InitialUpstreamRate: 44},
	}

	err, limit := state.MatchTargetLimit(limits, "foo", "bar")
	if err != nil {
		t.Fatalf("MatchTargetLimit returned error: %v", err)
	}
	if limit.InitialDownstreamRate != 11 || limit.InitialUpstreamRate != 22 {
		t.Fatalf("unexpected limit returned: %+v", limit)
	}
}

func TestMatchTargetLimitFallsBackToTargetOnly(t *testing.T) {
	state := RateLimiterInterfaceState{}
	limits := []config.TargetRateLimit{
		{Target: "foo", Subtarget: "", InitialDownstreamRate: 33, InitialUpstreamRate: 44},
	}

	err, limit := state.MatchTargetLimit(limits, "foo", "baz")
	if err != nil {
		t.Fatalf("MatchTargetLimit returned error: %v", err)
	}
	if limit.InitialDownstreamRate != 33 || limit.InitialUpstreamRate != 44 {
		t.Fatalf("unexpected limit returned: %+v", limit)
	}
}

func TestCleanupMessagesDropsExpiredEntries(t *testing.T) {
	now := time.Now()
	state := RateLimiterInterfaceState{
		FromClient: []RateLimiterMessage{
			{Timestamp: now.Add(-20 * time.Minute)},
			{Timestamp: now.Add(-5 * time.Minute)},
		},
		FromServer: []RateLimiterMessage{
			{Timestamp: now.Add(-30 * time.Minute)},
			{Timestamp: now.Add(-2 * time.Minute)},
		},
	}

	state.CleanupMessages(now.Add(-15 * time.Minute))

	if got := len(state.FromClient); got != 1 {
		t.Fatalf("expected 1 client message after cleanup, got %d", got)
	}
	if got := len(state.FromServer); got != 1 {
		t.Fatalf("expected 1 server message after cleanup, got %d", got)
	}
	if !state.FromClient[0].Timestamp.After(now.Add(-15 * time.Minute)) {
		t.Fatalf("client cleanup kept an expired message")
	}
	if !state.FromServer[0].Timestamp.After(now.Add(-15 * time.Minute)) {
		t.Fatalf("server cleanup kept an expired message")
	}
}

func TestUpdateClientSignaledRatesAppliesLatestMessage(t *testing.T) {
	state := RateLimiterInterfaceState{
		FromClient: []RateLimiterMessage{
			{Message: newTestMessage(t, 1, "foo", "bar", 11, 22, 33, 44)},
		},
	}

	updated, err := state.UpdateClientSignaledRates()
	if err != nil {
		t.Fatalf("UpdateClientSignaledRates returned error: %v", err)
	}
	if !updated {
		t.Fatalf("UpdateClientSignaledRates did not update any rates")
	}

	if state.ClientLimits.MinDownstreamRate != 11 {
		t.Fatalf("expected MinDownstreamRate 11, got %d", state.ClientLimits.MinDownstreamRate)
	}
	if state.ClientLimits.MinUpstreamRate != 22 {
		t.Fatalf("expected MinUpstreamRate 22, got %d", state.ClientLimits.MinUpstreamRate)
	}
	if state.ClientLimits.MaxDownstreamRate != 33 {
		t.Fatalf("expected MaxDownstreamRate 33, got %d", state.ClientLimits.MaxDownstreamRate)
	}
	if state.ClientLimits.MaxUpstreamRate != 44 {
		t.Fatalf("expected MaxUpstreamRate 44, got %d", state.ClientLimits.MaxUpstreamRate)
	}
}

func TestUpdateSettingsUsesTargetDefaults(t *testing.T) {
	msg := newTestMessage(t, 7, "foo", "bar", 0, 0, 0, 0)
	state := RateLimiterInterfaceState{
		FromClient: []RateLimiterMessage{{Message: msg}},
	}
	limits := []config.TargetRateLimit{
		{
			Target:                "foo",
			Subtarget:             "bar",
			InitialDownstreamRate: 123,
			InitialUpstreamRate:   456,
			MinDownstreamRate:     100,
			MaxDownstreamRate:     1000,
			MinUpstreamRate:       200,
			MaxUpstreamRate:       2000,
		},
	}

	updated := state.UpdateSettings(limits)

	if updated.LocalTargetRate.DownstreamRate != 123 {
		t.Fatalf("expected downstream rate 123, got %d", updated.LocalTargetRate.DownstreamRate)
	}
	if updated.LocalTargetRate.UpstreamRate != 456 {
		t.Fatalf("expected upstream rate 456, got %d", updated.LocalTargetRate.UpstreamRate)
	}
	if updated.LocalLimits.MinDownstreamRate != 100 || updated.LocalLimits.MaxDownstreamRate != 1000 {
		t.Fatalf("unexpected downstream local limits: %+v", updated.LocalLimits)
	}
	if updated.LocalLimits.MinUpstreamRate != 200 || updated.LocalLimits.MaxUpstreamRate != 2000 {
		t.Fatalf("unexpected upstream local limits: %+v", updated.LocalLimits)
	}
	if updated.LastUpdateSequenceNumber != 7 {
		t.Fatalf("expected sequence number 7, got %d", updated.LastUpdateSequenceNumber)
	}
}

func TestGetResponseMessageCopiesCurrentRates(t *testing.T) {
	msg := newTestMessage(t, 9, "foo", "bar", 0, 0, 0, 0)
	rl := RateLimiter{
		state: map[string]RateLimiterInterfaceState{
			"eth0": {
				FromClient: []RateLimiterMessage{{Message: msg}},
				LocalTargetRate: RateLimiterTargetRate{
					DownstreamRate: 321,
					UpstreamRate:   654,
				},
				LastUpdateSequenceNumber: 9,
			},
		},
	}

	response, err := rl.GetResponseMessage("eth0")
	if err != nil {
		t.Fatalf("GetResponseMessage returned error: %v", err)
	}

	if got := getUintField(t, response, "SequenceNumber"); got != 10 {
		t.Fatalf("expected sequence number 10, got %d", got)
	}
	if got := getUintField(t, response, "DownstreamTarget"); got != 321 {
		t.Fatalf("expected downstream current 321, got %d", got)
	}
	if got := getUintField(t, response, "UpstreamTarget"); got != 654 {
		t.Fatalf("expected upstream current 654, got %d", got)
	}
	if got := getUintField(t, response, "DownstreamConfigured"); got != 321 {
		t.Fatalf("expected downstream configured 321, got %d", got)
	}
}
