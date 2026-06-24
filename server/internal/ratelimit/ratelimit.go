package ratelimit

import (
	"bytes"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"fastd-server-side-ratelimit/internal/config"
	"fastd-server-side-ratelimit/internal/protocol"
)

type RateLimitEventType string

const (
	PacketLossUpstream   RateLimitEventType = "packet_loss_upstream"
	PacketLossDownstream RateLimitEventType = "packet_loss_downstream"
	SystemLoad           RateLimitEventType = "system_load"
)

type RateLimiterMessage struct {
	Message   protocol.Message
	Timestamp time.Time
}

type RateLimiterInterfaceLimits struct {
	MinDownstreamRate uint32
	MinUpstreamRate   uint32

	MaxDownstreamRate uint32
	MaxUpstreamRate   uint32
}

type RateLimiterInterfaceSettings struct {
	DownstreamRate uint32
	UpstreamRate   uint32

	MinDownstreamRate uint32
	MinUpstreamRate   uint32

	MaxDownstreamRate uint32
	MaxUpstreamRate   uint32
}

type RateLimiterInterfaceState struct {
	Ifname     string
	FromClient []RateLimiterMessage
	FromServer []RateLimiterMessage

	Settings RateLimiterInterfaceSettings

	LocalLimits  RateLimiterInterfaceLimits
	ClientLimits RateLimiterInterfaceLimits

	LastUpdateTime           time.Time
	LastUpdateSequenceNumber uint32

	LastRateReductionLoad time.Time
	LastRateIncreaseLoad  time.Time

	LastRateIncrease  map[RateLimitEventType]time.Time
	LastRateReduction map[RateLimitEventType]time.Time
}

func (s *RateLimiterInterfaceState) CleanupMessages(threshold time.Time) {
	var cleanedFromClient []RateLimiterMessage
	for _, msg := range s.FromClient {
		if msg.Timestamp.After(threshold) {
			cleanedFromClient = append(cleanedFromClient, msg)
		}
	}
	s.FromClient = cleanedFromClient

	var cleanedFromServer []RateLimiterMessage
	for _, msg := range s.FromServer {
		if msg.Timestamp.After(threshold) {
			cleanedFromServer = append(cleanedFromServer, msg)
		}
	}
	s.FromServer = cleanedFromServer
}

func (s RateLimiterInterfaceState) AddMessage(msg protocol.Message, fromClient bool) RateLimiterInterfaceState {
	entry := RateLimiterMessage{Message: msg, Timestamp: time.Now()}
	if fromClient {
		s.FromClient = append(s.FromClient, entry)
	} else {
		s.FromServer = append(s.FromServer, entry)
		// Update SequenceNumber for sent message to track responses
		s.LastUpdateSequenceNumber = msg.SequenceNumber
	}
	return s
}

func (s RateLimiterInterfaceState) MatchTargetLimit(targetSettings []config.TargetRateLimit, target string, subtarget string) (error, config.TargetRateLimit) {
	for _, ts := range targetSettings {
		if ts.Target == target {
			if ts.Subtarget == "" || ts.Subtarget == subtarget {
				return nil, ts
			}
		}
	}

	return fmt.Errorf("no target rate limit found for target %q and subtarget %q", target, subtarget), config.TargetRateLimit{}
}
func (s RateLimiterInterfaceState) GetTargetRateLimit(targetSettings []config.TargetRateLimit) (error, config.TargetRateLimit) {
	target, subtarget := s.GetTargetAndSubtarget()

	// Try with target and subtarget first, then fallback to target only, then fallback to default
	err, targetLimit := s.MatchTargetLimit(targetSettings, target, subtarget)
	if err == nil {
		return nil, targetLimit
	}

	err, targetLimit = s.MatchTargetLimit(targetSettings, target, "")
	if err == nil {
		return nil, targetLimit
	}

	err, targetLimit = s.MatchTargetLimit(targetSettings, "", "")
	if err == nil {
		return nil, targetLimit
	}

	return fmt.Errorf("no target rate limit found for target %q and subtarget %q", target, subtarget), config.TargetRateLimit{}
}

func (s *RateLimiterInterfaceState) UpdateClientSignaledRates() error {
	if len(s.FromClient) == 0 {
		return fmt.Errorf("no client messages to update rates from")
	}

	latestMessage := s.FromClient[len(s.FromClient)-1].Message

	/* Update Minima and Maxima based on latest client message */
	if latestMessage.DownstreamMin != 0 {
		s.ClientLimits.MinDownstreamRate = latestMessage.DownstreamMin
	}
	if latestMessage.UpstreamMin != 0 {
		s.ClientLimits.MinUpstreamRate = latestMessage.UpstreamMin
	}
	if latestMessage.DownstreamMax != 0 {
		s.ClientLimits.MaxDownstreamRate = latestMessage.DownstreamMax
	}
	if latestMessage.UpstreamMax != 0 {
		s.ClientLimits.MaxUpstreamRate = latestMessage.UpstreamMax
	}

	return nil
}

func (s RateLimiterInterfaceState) GetTargetAndSubtarget() (string, string) {
	if len(s.FromClient) == 0 {
		return "", ""
	}
	latestMessage := s.FromClient[len(s.FromClient)-1].Message
	target := string(bytes.TrimRight(latestMessage.MachineInformation.Target[:], "\x00"))
	subtarget := string(bytes.TrimRight(latestMessage.MachineInformation.Subtarget[:], "\x00"))
	return target, subtarget
}

func (s RateLimiterInterfaceState) UpdateSettings(targetSettings []config.TargetRateLimit) RateLimiterInterfaceState {
	s.LastUpdateTime = time.Now()
	/* Get latest client message to determine target and subtarget. */
	if len(s.FromClient) == 0 {
		return s
	}
	latestMessage := s.FromClient[len(s.FromClient)-1].Message

	/* Update Client Signaled Rates to set Min/Max values */
	s.UpdateClientSignaledRates()

	/* Update Local Limits based on target/subtarget settings */
	targetErr, targetLimit := s.GetTargetRateLimit(targetSettings)
	if targetErr == nil {
		s.LocalLimits.MinDownstreamRate = targetLimit.MinDownstreamRate
		s.LocalLimits.MaxDownstreamRate = targetLimit.MaxDownstreamRate
		s.LocalLimits.MinUpstreamRate = targetLimit.MinUpstreamRate
		s.LocalLimits.MaxUpstreamRate = targetLimit.MaxUpstreamRate
	}

	s.LastUpdateSequenceNumber = latestMessage.SequenceNumber

	/* Determine starting point. */
	if s.Settings.DownstreamRate == 0 && s.Settings.UpstreamRate == 0 {
		/* By default, use the target/subtarget defaults if available, otherwise use the client signaled rates. */
		if targetErr == nil {
			s.Settings.DownstreamRate = targetLimit.InitialDownstreamRate
			s.Settings.UpstreamRate = targetLimit.InitialUpstreamRate
		}

		/* Check if client has upper limits set, and if so, use them as starting point. */
		if s.ClientLimits.MaxDownstreamRate > 0 {
			s.Settings.DownstreamRate = s.ClientLimits.MaxDownstreamRate
		}
		if s.ClientLimits.MaxUpstreamRate > 0 {
			s.Settings.UpstreamRate = s.ClientLimits.MaxUpstreamRate
		}
	} else {
		// ToDo: Dynamic rate adaption here
	}

	/* Ensure configured rates are within the min/max bounds */
	if s.Settings.DownstreamRate < s.LocalLimits.MinDownstreamRate {
		s.Settings.DownstreamRate = s.LocalLimits.MinDownstreamRate
	}
	if s.LocalLimits.MaxDownstreamRate > 0 && s.Settings.DownstreamRate > s.LocalLimits.MaxDownstreamRate {
		s.Settings.DownstreamRate = s.LocalLimits.MaxDownstreamRate
	}
	if s.Settings.UpstreamRate < s.LocalLimits.MinUpstreamRate {
		s.Settings.UpstreamRate = s.LocalLimits.MinUpstreamRate
	}
	if s.LocalLimits.MaxUpstreamRate > 0 && s.Settings.UpstreamRate > s.LocalLimits.MaxUpstreamRate {
		s.Settings.UpstreamRate = s.LocalLimits.MaxUpstreamRate
	}

	return s
}

type RateLimiter struct {
	mu sync.Mutex

	TargetLimits []config.TargetRateLimit

	// state holds the last 15 Minutes of messages per client, indexed by the interface name
	state             map[string]RateLimiterInterfaceState
	MinDownstreamRate uint32
	MinUpstreamRate   uint32
	MaxDownstreamRate uint32
	MaxUpstreamRate   uint32
	ShaperScript      string
}

func (rl *RateLimiter) initInterfaceState(ifname string) {
	if rl.state == nil {
		rl.state = make(map[string]RateLimiterInterfaceState)
	}

	if _, ok := rl.state[ifname]; !ok {
		rl.state[ifname] = RateLimiterInterfaceState{
			Ifname:            ifname,
			LastRateIncrease:  make(map[RateLimitEventType]time.Time),
			LastRateReduction: make(map[RateLimitEventType]time.Time),

			Settings: RateLimiterInterfaceSettings{
				DownstreamRate:    0,
				UpstreamRate:      0,
				MinDownstreamRate: rl.MinDownstreamRate,
				MinUpstreamRate:   rl.MinUpstreamRate,
				MaxDownstreamRate: rl.MaxDownstreamRate,
				MaxUpstreamRate:   rl.MaxUpstreamRate,
			},
		}
	}
}

func (rl *RateLimiter) cleanupInterfaceMessages(ifname string) {

	ifs := rl.state[ifname]
	ifs.CleanupMessages(time.Now().Add(-15 * time.Minute))
	rl.state[ifname] = ifs
}

func (rl *RateLimiter) Cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	for ifname := range rl.state {
		rl.cleanupInterfaceMessages(ifname)
	}
}

func (rl *RateLimiter) AddMessage(msg protocol.Message, ifname string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.initInterfaceState(ifname)

	state := rl.state[ifname]
	state = state.AddMessage(msg, true)
	rl.state[ifname] = state
}

func (rl *RateLimiter) UpdateSettings(ifname string) error {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.initInterfaceState(ifname)

	state := rl.state[ifname]
	state = state.UpdateSettings(rl.TargetLimits)
	rl.state[ifname] = state

	return nil
}

func (rl *RateLimiter) ApplyShaper(ifname string) error {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.initInterfaceState(ifname)

	if _, ok := rl.state[ifname]; !ok {
		return fmt.Errorf("interface %q not found in rate limiter state", ifname)
	}

	downstreamRate := rl.state[ifname].Settings.DownstreamRate
	upstreamRate := rl.state[ifname].Settings.UpstreamRate

	// Prepare environment to execute shaper script
	env := []string{
		fmt.Sprintf("FSSRL_DOWNSTREAM_RATE=%d", downstreamRate),
		fmt.Sprintf("FSSRL_UPSTREAM_RATE=%d", upstreamRate),
		fmt.Sprintf("FSSRL_TARGET_IF=%s", ifname),
		fmt.Sprintf("FSSRL_ROLE=server"),
	}

	cmd := exec.Command(rl.ShaperScript)
	cmd.Env = append(cmd.Env, env...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to execute shaper script: %v, output: %s", err, string(output))
	}

	return nil
}

func (rl *RateLimiter) GetResponseMessage(ifname string) (protocol.Message, error) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.initInterfaceState(ifname)

	if _, ok := rl.state[ifname]; !ok {
		return protocol.Message{}, fmt.Errorf("interface %q not found in rate limiter state", ifname)
	}

	// Get Last message and deepcopy
	lastMessage := rl.state[ifname].FromClient[len(rl.state[ifname].FromClient)-1].Message
	responseMessage, err := lastMessage.Clone()
	if err != nil {
		return protocol.Message{}, fmt.Errorf("failed to clone message for response: %v", err)
	}

	downstreamRate := rl.state[ifname].Settings.DownstreamRate
	upstreamRate := rl.state[ifname].Settings.UpstreamRate

	responseMessage.DownstreamCurrent = downstreamRate
	responseMessage.UpstreamCurrent = upstreamRate
	responseMessage.DownstreamConfigured = downstreamRate
	responseMessage.SequenceNumber = rl.state[ifname].LastUpdateSequenceNumber + 1

	return responseMessage, nil
}

func (rl *RateLimiter) RegisterSentMessage(ifname string, msg protocol.Message) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.initInterfaceState(ifname)

	if _, ok := rl.state[ifname]; !ok {
		return
	}

	state := rl.state[ifname]
	state = state.AddMessage(msg, false)
	rl.state[ifname] = state
}
