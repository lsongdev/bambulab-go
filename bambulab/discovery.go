package bambulab

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/lsongdev/ssdp-go/ssdp"
)

const discoveryWindow = 15 * time.Second

// DiscoveredPrinter is a printer announced on the local network.
// IP is taken from the UDP source; ID is the printer serial from USN.
type DiscoveredPrinter struct {
	ID    string
	IP    net.IP
	Name  string
	Model string
}

// DiscoverPrinter waits briefly for a LAN announcement matching serial.
// The SSDP listener closes before this function returns.
func DiscoverPrinter(ctx context.Context, serial string) (DiscoveredPrinter, error) {
	if serial == "" {
		return DiscoveredPrinter{}, fmt.Errorf("bambulab: printer serial is required")
	}
	var found DiscoveredPrinter
	err := listenPrinters(ctx, discoveryWindow, func(device DiscoveredPrinter) bool {
		if device.ID != serial {
			return false
		}
		found = device
		return true
	})
	if err != nil {
		return DiscoveredPrinter{}, err
	}
	if found.ID == "" {
		return found, fmt.Errorf("bambulab: printer %s was not discovered on this LAN", serial)
	}
	return found, nil
}

// DiscoverPrinters collects LAN announcements during window (default 15s).
// Only packets received before the deadline are returned; this is a one-shot call.
func DiscoverPrinters(ctx context.Context, window time.Duration) ([]DiscoveredPrinter, error) {
	if window < 0 {
		return nil, fmt.Errorf("bambulab: discovery window must not be negative")
	}
	if window == 0 {
		window = discoveryWindow
	}
	devices := make([]DiscoveredPrinter, 0)
	seen := make(map[string]bool)
	err := listenPrinters(ctx, window, func(device DiscoveredPrinter) bool {
		if !seen[device.ID] {
			devices = append(devices, device)
			seen[device.ID] = true
		}
		return false
	})
	return devices, err
}

func listenPrinters(ctx context.Context, window time.Duration, visit func(DiscoveredPrinter) bool) error {
	client := ssdp.NewClient(ssdp.Config{Port: 2021, Address: "255.255.255.255"})
	listenCtx, cancel := context.WithTimeout(ctx, window)
	defer cancel()
	err := client.ListenNotifications(listenCtx, func(packet ssdp.Packet) bool {
		device, ok := parseAnnouncement(packet)
		return ok && visit(device)
	})
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return nil
	}
	return err
}

func parseAnnouncement(packet ssdp.Packet) (DiscoveredPrinter, bool) {
	if packet.Source == nil || packet.Source.IP.To4() == nil ||
		packet.Header.Get("NT") != "urn:bambulab-com:device:3dprinter:1" {
		return DiscoveredPrinter{}, false
	}
	id := strings.TrimSpace(packet.Header.Get("USN"))
	if id == "" {
		return DiscoveredPrinter{}, false
	}
	return DiscoveredPrinter{
		ID: id, IP: append(net.IP(nil), packet.Source.IP...),
		Name:  packet.Header.Get("DevName.bambu.com"),
		Model: packet.Header.Get("DevModel.bambu.com"),
	}, true
}
