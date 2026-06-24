package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"fastd-server-side-ratelimit/internal/config"
	"fastd-server-side-ratelimit/internal/protocol"
	"fastd-server-side-ratelimit/internal/ratelimit"

	"golang.org/x/net/ipv6"
)

const (
	protocolPort   = 42453
	serverAddress  = "fe80::f421:d:1"
	clientAddress  = "fe80::f421:d:2"
	messageSize    = protocol.MessageSize
	defaultTimeout = 5 * time.Second
	updateInterval = 15 * time.Second
)

var rateLimiter ratelimit.RateLimiter

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	log.Printf("starting server on [%s]:%d", serverAddress, protocolPort)

	// Load Config
	if len(os.Args) < 2 {
		log.Fatal("config file path argument is required")
	}
	s := flag.String("config", "", "path to config file")
	flag.Parse()
	cfg, err := config.Load(*s)
	if err != nil {
		log.Fatal("failed to load config: %v", err)
	}

	rateLimiter = ratelimit.RateLimiter{
		MinDownstreamRate: cfg.Bandwith.Min.Download,
		MinUpstreamRate:   cfg.Bandwith.Min.Upload,
		MaxDownstreamRate: cfg.Bandwith.Max.Download,
		MaxUpstreamRate:   cfg.Bandwith.Max.Upload,
		ShaperScript:      cfg.ShaperScript,
		TargetLimits:      cfg.TargetLimits,
	}

	err = runServer(cfg)
	if err != nil {
		log.Fatal(err)
	}
}

func runServer(cfg config.Config) error {
	conn, err := openServerSocket()
	if err != nil {
		return err
	}
	defer conn.Close()

	// Wrap the UDPConn with ipv6 PacketConn to receive control messages
	pconn := ipv6.NewPacketConn(conn)
	defer pconn.Close()
	pconn.SetControlMessage(ipv6.FlagInterface, true)

	buffer := make([]byte, messageSize)
	for {
		n, cm, _, err := pconn.ReadFrom(buffer)
		if err != nil {
			return err
		}

		message, err := protocol.MessageFromBytes(buffer[:n])
		if err != nil {
			continue
		}

		if cm == nil {
			continue
		}

		ifi, err := net.InterfaceByIndex(cm.IfIndex)
		if err != nil {
			continue
		}

		if cfg.InterfacePrefix != "" && !strings.HasPrefix(ifi.Name, cfg.InterfacePrefix) {
			continue
		}
		if cfg.InterfaceSuffix != "" && !strings.HasSuffix(ifi.Name, cfg.InterfaceSuffix) {
			continue
		}
		handleMesage(message, ifi, pconn)
	}
}

func openServerSocket() (*net.UDPConn, error) {
	return net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6unspecified, Port: protocolPort})
}

func sendResponse(message protocol.Message, ifname string, pconn *ipv6.PacketConn) error {
	raddr := &net.UDPAddr{IP: net.ParseIP(clientAddress), Port: protocolPort + 1, Zone: ifname}

	data, err := message.MarshalBinary()
	if err != nil {
		return fmt.Errorf("failed to marshal message: %v", err)
	}

	_, err = pconn.WriteTo(data, nil, raddr)
	if err != nil {
		return fmt.Errorf("failed to send response: %v", err)
	}

	return nil
}

func handleMesage(message protocol.Message, ifi *net.Interface, pconn *ipv6.PacketConn) error {
	// ToDo: Cleanup somewhere else
	rateLimiter.Cleanup()

	// Add message
	rateLimiter.AddMessage(message, ifi.Name)

	// Update settings based on the current state
	rateLimiter.UpdateSettings(ifi.Name)

	// Apply Shaper
	rateLimiter.ApplyShaper(ifi.Name)

	// Craft response packet. Base on latest packet from client - Marshal and unmarshal to clone
	responseMessage, err := rateLimiter.GetResponseMessage(ifi.Name)
	if err != nil {
		return fmt.Errorf("failed to create response message: %v", err)
	}
	if err != nil {
		return fmt.Errorf("failed to clone message for response: %v", err)
	}

	fmt.Printf("Received message from interface %s: %s\n", ifi.Name, message.String())
	fmt.Printf("Sending response to interface %s: %s\n", ifi.Name, responseMessage.String())

	// Send response back to client
	err = sendResponse(responseMessage, ifi.Name, pconn)
	if err != nil {
		return fmt.Errorf("failed to send response: %v", err)
	}

	// Register sent message to track responses
	rateLimiter.RegisterSentMessage(ifi.Name, responseMessage)

	return nil
}
