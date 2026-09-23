package bambulab

import (
	"context"
	"github.com/lsongdev/ssdp-go/ssdp"
	"net"
	"testing"
	"time"
)

func TestParseAnnouncement(t *testing.T) {
	packet := []byte("NOTIFY * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nNT: urn:bambulab-com:device:3dprinter:1\r\nUSN: SERIAL\r\nLocation: 192.168.1.99\r\nDevName.bambu.com: Test printer\r\nDevModel.bambu.com: N1\r\n\r\n")
	parsed, err := ssdp.ParsePacket(packet, &net.UDPAddr{IP: net.ParseIP("192.168.1.12")})
	if err != nil {
		t.Fatal(err)
	}
	device, ok := parseAnnouncement(parsed)
	if !ok || device.ID != "SERIAL" || device.IP.String() != "192.168.1.12" || device.Name != "Test printer" || device.Model != "N1" {
		t.Fatalf("unexpected announcement: %+v, valid=%v", device, ok)
	}
	if _, ok := parseAnnouncement(ssdp.Packet{Header: make(map[string][]string)}); ok {
		t.Fatal("accepted a search request")
	}
}

func TestDiscoverPrinterValidation(t *testing.T) {
	if _, err := DiscoverPrinter(context.Background(), ""); err == nil {
		t.Fatal("accepted empty serial")
	}
	if _, err := DiscoverPrinters(context.Background(), -time.Second); err == nil {
		t.Fatal("accepted negative window")
	}
}
