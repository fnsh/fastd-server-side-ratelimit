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

type RateLimiterTargetRate struct {
	DownstreamRate uint32
	UpstreamRate   uint32
}

type RateLimiterRemoteRequests struct {
	DisableUpstreamShaping          bool
	DisableUpstreamShapingApplied   bool
	DisableDownstreamShaping        bool
	DisableDownstreamShapingApplied bool
}

type RateLimiterInterfaceState struct {
	Ifname     string
	FromClient []RateLimiterMessage
	FromServer []RateLimiterMessage

	RemoteRequests RateLimiterRemoteRequests

	LocalTargetRate  RateLimiterTargetRate
	RemoteTargetRate RateLimiterTargetRate

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

func (s *RateLimiterInterfaceState) UpdateClientSignaledRates() (bool, error) {
	updated := false
	if len(s.FromClient) == 0 {
		return updated, fmt.Errorf("no client messages to update rates from")
	}

	latestMessage := s.FromClient[len(s.FromClient)-1].Message

	if latestMessage.DownstreamMin != s.ClientLimits.MinDownstreamRate {
		s.ClientLimits.MinDownstreamRate = latestMessage.DownstreamMin
		updated = true
	}
	if latestMessage.UpstreamMin != s.ClientLimits.MinUpstreamRate {
		s.ClientLimits.MinUpstreamRate = latestMessage.UpstreamMin
		updated = true
	}
	if latestMessage.DownstreamMax != s.ClientLimits.MaxDownstreamRate {
		s.ClientLimits.MaxDownstreamRate = latestMessage.DownstreamMax
		updated = true
	}
	if latestMessage.UpstreamMax != s.ClientLimits.MaxUpstreamRate {
		s.ClientLimits.MaxUpstreamRate = latestMessage.UpstreamMax
		updated = true
	}
	if latestMessage.DownstreamTarget != s.RemoteTargetRate.DownstreamRate {
		s.RemoteTargetRate.DownstreamRate = latestMessage.DownstreamTarget
		updated = true
	}
	if latestMessage.UpstreamTarget != s.RemoteTargetRate.UpstreamRate {
		s.RemoteTargetRate.UpstreamRate = latestMessage.UpstreamTarget
		updated = true
	}

	// Global configuration flags - Requests
	s.RemoteRequests.DisableDownstreamShaping = (latestMessage.GlobalConfigurationFlags & (1 << 2)) != 0
	s.RemoteRequests.DisableUpstreamShaping = (latestMessage.GlobalConfigurationFlags & (1 << 3)) != 0

	return updated, nil
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
	updated, _ := s.UpdateClientSignaledRates()

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
	if (s.LocalTargetRate.DownstreamRate == 0 && s.LocalTargetRate.UpstreamRate == 0) || updated {
		/* In the update case, we might have old limits configured.
		 * Set to 0 here to indicate shaper shall be disabled if defaults are not set.
		 */
		s.LocalTargetRate.DownstreamRate = 0
		s.LocalTargetRate.UpstreamRate = 0

		/* By default, use the target/subtarget defaults if available, otherwise use the client signaled rates. */
		if targetErr == nil {
			s.LocalTargetRate.DownstreamRate = targetLimit.InitialDownstreamRate
			s.LocalTargetRate.UpstreamRate = targetLimit.InitialUpstreamRate
		}
	} else {
		// ToDo: Dynamic rate adaption here
	}

	/* Check if client has requested to disable shaping */
	if s.RemoteRequests.DisableDownstreamShaping {
		s.LocalTargetRate.DownstreamRate = 0
		s.RemoteRequests.DisableDownstreamShapingApplied = true
	} else {
		/* Ensure the downstream shaper is adhering to the maximum downstream rate signalled by
		 * the client.
		 * If the client maximum rate undercuts our minimum rate, the server will later
		 * configure the local
		 * minimum rate.
		 */
		if s.ClientLimits.MaxDownstreamRate > 0 {
			s.LocalTargetRate.DownstreamRate = s.ClientLimits.MaxDownstreamRate
		}

		/* Enforce Limits configured on server side. */
		if s.LocalTargetRate.DownstreamRate < s.LocalLimits.MinDownstreamRate {
			s.LocalTargetRate.DownstreamRate = s.LocalLimits.MinDownstreamRate
		}
		if s.LocalLimits.MaxDownstreamRate > 0 && s.LocalTargetRate.DownstreamRate > s.LocalLimits.MaxDownstreamRate {
			s.LocalTargetRate.DownstreamRate = s.LocalLimits.MaxDownstreamRate
		}
		s.RemoteRequests.DisableDownstreamShapingApplied = false
	}

	if s.RemoteRequests.DisableUpstreamShaping {
		s.LocalTargetRate.UpstreamRate = 0
		s.RemoteRequests.DisableUpstreamShapingApplied = true
	} else {
		/* This might become useful in the future in case we limit upstream rate on server optionally.
		 * For now, this is a no-op.
		 */
		if s.ClientLimits.MaxUpstreamRate > 0 {
			s.LocalTargetRate.UpstreamRate = s.ClientLimits.MaxUpstreamRate
		}

		/* As above (so below), the upstream rate limiting is not used for now. */
		if s.LocalTargetRate.UpstreamRate < s.LocalLimits.MinUpstreamRate {
			s.LocalTargetRate.UpstreamRate = s.LocalLimits.MinUpstreamRate
		}
		if s.LocalLimits.MaxUpstreamRate > 0 && s.LocalTargetRate.UpstreamRate > s.LocalLimits.MaxUpstreamRate {
			s.LocalTargetRate.UpstreamRate = s.LocalLimits.MaxUpstreamRate
		}
		s.RemoteRequests.DisableUpstreamShapingApplied = false
	}

	return s
}

type RateLimiter struct {
	mu sync.Mutex

	TargetLimits []config.TargetRateLimit

	// state holds the last 15 Minutes of messages per client, indexed by the interface name
	state        map[string]RateLimiterInterfaceState
	ShaperScript string
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

			LocalTargetRate: RateLimiterTargetRate{},
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

	downstreamRate := rl.state[ifname].LocalTargetRate.DownstreamRate
	upstreamRate := rl.state[ifname].LocalTargetRate.UpstreamRate

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

	localTargetRate := rl.state[ifname].LocalTargetRate
	localLimits := rl.state[ifname].LocalLimits

	responseMessage.GlobalConfigurationFlags = 0
	// Global configuration flags - Reports
	if localTargetRate.DownstreamRate == 0 {
		responseMessage.GlobalConfigurationFlags |= 1 << 1 // Bit 1: Server Downstream shaping disabled
	}
	if localTargetRate.UpstreamRate == 0 {
		responseMessage.GlobalConfigurationFlags |= 1 << 0 // Bit 0: Server Upstream shaping disabled
	}

	// Global configuration flags - Requests
	// ToDo: No requests for now

	// Locally applied
	responseMessage.DownstreamTarget = localTargetRate.DownstreamRate
	responseMessage.DownstreamConfigured = localTargetRate.DownstreamRate

	// Remote client signaled
	responseMessage.UpstreamTarget = localTargetRate.UpstreamRate
	responseMessage.UpstreamConfigured = 0 // No Upstream shaping applied on server side for now

	// Local limits
	responseMessage.DownstreamMin = localLimits.MinDownstreamRate
	responseMessage.DownstreamMax = localLimits.MaxDownstreamRate
	responseMessage.UpstreamMin = localLimits.MinUpstreamRate
	responseMessage.UpstreamMax = localLimits.MaxUpstreamRate

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
