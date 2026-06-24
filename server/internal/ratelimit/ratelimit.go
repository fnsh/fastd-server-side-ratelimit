package ratelimit

import (
	"fmt"
	"log"
	"os/exec"
	"sync"
	"time"

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

	LastUpdateTime           time.Time
	LastUpdateSequenceNumber uint32

	LastRateReductionLoad time.Time
	LastRateIncreaseLoad  time.Time

	LastRateIncrease  map[RateLimitEventType]time.Time
	LastRateReduction map[RateLimitEventType]time.Time
}

func (s RateLimiterInterfaceState) CleanupMessages(threshold time.Time) {
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

func (s RateLimiterInterfaceState) getDefaultSettings(target string, subtarget string) (error, RateLimiterInterfaceSettings) {
	targetDefaultMapping := map[string]map[string]RateLimiterInterfaceSettings{
		"ath79": {
			"default": {DownstreamRate: 10000, UpstreamRate: 5000},
		},
		"ipq40xx": {
			"default": {DownstreamRate: 25000, UpstreamRate: 10000},
		},
		"ramips": {
			"mt7621":  {DownstreamRate: 25000, UpstreamRate: 12500},
			"default": {DownstreamRate: 10000, UpstreamRate: 5000},
		},
		"default": {
			"default": {DownstreamRate: 90000, UpstreamRate: 30000},
		},
	}

	if subtargetSettings, ok := targetDefaultMapping[target]; ok {
		if settings, ok := subtargetSettings[subtarget]; ok {
			s.Settings = settings
			return nil, settings
		}
		if settings, ok := subtargetSettings["default"]; ok {
			s.Settings = settings
			return nil, settings
		}
	}

	return nil, targetDefaultMapping["default"]["default"]
}

func (s RateLimiterInterfaceState) updateRateAbsolute(rateDown int32, rateUp int32, reason RateLimitEventType) (error, RateLimiterInterfaceState) {
	if rateDown < 0 && rateUp > 0 || rateDown > 0 && rateUp < 0 {
		return fmt.Errorf("cannot update upstream and downstream rates in different directions at the same time"), s
	}

	if rateDown < 0 {
		s.Settings.DownstreamRate = uint32(int32(s.Settings.DownstreamRate) + rateDown)
	} else if rateDown > 0 {
		s.Settings.DownstreamRate = uint32(int32(s.Settings.DownstreamRate) + rateDown)
	}

	if rateUp < 0 {
		s.Settings.UpstreamRate = uint32(int32(s.Settings.UpstreamRate) + rateUp)
	} else if rateUp > 0 {
		s.Settings.UpstreamRate = uint32(int32(s.Settings.UpstreamRate) + rateUp)
	}

	// Check mins and max are satisfied
	if s.Settings.DownstreamRate < s.Settings.MinDownstreamRate {
		s.Settings.DownstreamRate = s.Settings.MinDownstreamRate
	}
	if s.Settings.DownstreamRate > s.Settings.MaxDownstreamRate {
		s.Settings.DownstreamRate = s.Settings.MaxDownstreamRate
	}
	if s.Settings.UpstreamRate < s.Settings.MinUpstreamRate {
		s.Settings.UpstreamRate = s.Settings.MinUpstreamRate
	}
	if s.Settings.UpstreamRate > s.Settings.MaxUpstreamRate {
		s.Settings.UpstreamRate = s.Settings.MaxUpstreamRate
	}

	if rateDown < 0 || rateUp < 0 {
		s.LastRateIncrease[reason] = time.Now()
	} else if rateDown > 0 || rateUp > 0 {
		s.LastRateReduction[reason] = time.Now()
	}

	return nil, s
}

func (s RateLimiterInterfaceState) updateRateRelative(percentageDown float64, percentageUp float64, reason RateLimitEventType) (error, RateLimiterInterfaceState) {
	if percentageDown < 0 && percentageUp > 0 || percentageDown > 0 && percentageUp < 0 {
		return fmt.Errorf("cannot update upstream and downstream rates in different directions at the same time"), s
	}

	absoluteDiffDown := int32(float64(s.Settings.DownstreamRate) * percentageDown)
	absoluteDiffUp := int32(float64(s.Settings.UpstreamRate) * percentageUp)

	return s.updateRateAbsolute(absoluteDiffDown, absoluteDiffUp, reason)
}

func (s RateLimiterInterfaceState) UpdateSettings() RateLimiterInterfaceState {
	s.LastUpdateTime = time.Now()
	/* Get latest client message to determine target and subtarget. */
	if len(s.FromClient) == 0 {
		return s
	}
	latestMessage := s.FromClient[len(s.FromClient)-1].Message

	/* Update Minima and Maxima based on latest client message */
	if latestMessage.DownstreamMin != 0 {
		s.Settings.MinDownstreamRate = latestMessage.DownstreamMin
	}
	if latestMessage.UpstreamMin != 0 {
		s.Settings.MinUpstreamRate = latestMessage.UpstreamMin
	}
	if latestMessage.DownstreamMax != 0 {
		s.Settings.MaxDownstreamRate = latestMessage.DownstreamMax
	}
	if latestMessage.UpstreamMax != 0 {
		s.Settings.MaxUpstreamRate = latestMessage.UpstreamMax
	}

	s.LastUpdateSequenceNumber = latestMessage.SequenceNumber

	target := string(latestMessage.MachineInformation.Target[:])
	subtarget := string(latestMessage.MachineInformation.Subtarget[:])
	_, newSettings := s.getDefaultSettings(target, subtarget)

	/* Determine starting point. */
	if s.Settings.DownstreamRate == 0 && s.Settings.UpstreamRate == 0 {
		/* Check if settings are within minima and maxima. */
		if latestMessage.DownstreamMin != 0 && newSettings.DownstreamRate < latestMessage.DownstreamMin {
			newSettings.DownstreamRate = latestMessage.DownstreamMin
		} else if latestMessage.DownstreamMax != 0 && newSettings.DownstreamRate > latestMessage.DownstreamMax {
			newSettings.DownstreamRate = latestMessage.DownstreamMax
		}

		if latestMessage.UpstreamMin != 0 && newSettings.UpstreamRate < latestMessage.UpstreamMin {
			newSettings.UpstreamRate = latestMessage.UpstreamMin
		} else if latestMessage.UpstreamMax != 0 && newSettings.UpstreamRate > latestMessage.UpstreamMax {
			newSettings.UpstreamRate = latestMessage.UpstreamMax
		}

		log.Printf("Initial settings based on target/subtarget %s/%s: downstream %d kbps, upstream %d kbps", target, subtarget, newSettings.DownstreamRate, newSettings.UpstreamRate)

		s.Settings = newSettings
		return s
	}

	/* Check currently set rate satisfies current constraints */
	if s.Settings.DownstreamRate < s.Settings.MinDownstreamRate {
		s.Settings.DownstreamRate = s.Settings.MinDownstreamRate
		log.Printf("Updated downstream rate to %d kbps based on client message", s.Settings.DownstreamRate)
	}
	if s.Settings.UpstreamRate < s.Settings.MinUpstreamRate {
		s.Settings.UpstreamRate = s.Settings.MinUpstreamRate
		log.Printf("Updated upstream rate to %d kbps based on client message", s.Settings.UpstreamRate)
	}
	if s.Settings.DownstreamRate > s.Settings.MaxDownstreamRate {
		s.Settings.DownstreamRate = s.Settings.MaxDownstreamRate
		log.Printf("Updated downstream rate to %d kbps based on client message", s.Settings.DownstreamRate)
	}
	if s.Settings.UpstreamRate > s.Settings.MaxUpstreamRate {
		s.Settings.UpstreamRate = s.Settings.MaxUpstreamRate
		log.Printf("Updated upstream rate to %d kbps based on client message", s.Settings.UpstreamRate)
	}

	/* ToDo: Dynamic update */
	return s
}

type RateLimiter struct {
	mu sync.Mutex

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
	state = state.UpdateSettings()
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
