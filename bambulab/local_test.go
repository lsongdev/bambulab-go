package bambulab

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eclipse/paho.mqtt.golang/packets"
)

func testCertificates(t *testing.T) (*tls.Config, *tls.Config, *x509.Certificate) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, root, root, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	root, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "SERIAL"}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, NotBefore: root.NotBefore, NotAfter: root.NotAfter}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err = x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatal(err)
	}
	client, err := PrinterTLSConfig("SERIAL", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	if err != nil {
		t.Fatal(err)
	}
	server := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER, der}, PrivateKey: key}}}
	return server, client, leaf
}
func TestPrinterTLS(t *testing.T) {
	_, cfg, leaf := testCertificates(t)
	if err := cfg.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}); err != nil {
		t.Fatal(err)
	}
	copyLeaf := *leaf
	copyLeaf.Subject.CommonName = "WRONG"
	if err := cfg.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{&copyLeaf}}); err == nil {
		t.Fatal("accepted wrong serial")
	}
	copyLeaf = *leaf
	copyLeaf.NotAfter = time.Now().Add(-time.Hour)
	if err := cfg.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{&copyLeaf}}); err == nil {
		t.Fatal("accepted expired certificate")
	}
	_, other, _ := testCertificates(t)
	if err := other.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}); err == nil {
		t.Fatal("accepted untrusted CA")
	}
	if _, err := PrinterTLSConfig("SERIAL", []byte("garbage")); err == nil {
		t.Fatal("accepted invalid PEM")
	}
}
func framePacket(frame []byte) []byte {
	data := make([]byte, 16+len(frame))
	binary.LittleEndian.PutUint32(data, uint32(len(frame)))
	copy(data[16:], frame)
	return data
}

type fragmentReader struct{ reader io.Reader }

func (r fragmentReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.reader.Read(p)
}
func TestCameraFraming(t *testing.T) {
	jpeg := []byte{0xff, 0xd8, 1, 2, 3, 0xff, 0xd9}
	packet := framePacket(jpeg)
	frame, err := readJPEGFrame(fragmentReader{bytes.NewReader(packet)}, 1024)
	if err != nil || !bytes.Equal(frame, jpeg) {
		t.Fatal(frame, err)
	}
	for _, bad := range [][]byte{packet[:10], packet[:len(packet)-1], framePacket([]byte("xxxx")), framePacket([]byte{1})} {
		if _, err := readJPEGFrame(bytes.NewReader(bad), 1024); err == nil {
			t.Fatal("accepted malformed frame")
		}
	}
	if _, err := readJPEGFrame(bytes.NewReader(packet), 4); err == nil {
		t.Fatal("accepted oversize frame")
	}
	auth, err := cameraAuth("12345678")
	if err != nil || len(auth) != 80 || binary.LittleEndian.Uint32(auth[:4]) != 64 || binary.LittleEndian.Uint32(auth[4:8]) != 0x3000 || string(auth[16:20]) != "bblp" || string(auth[48:56]) != "12345678" {
		t.Fatal(auth, err)
	}
	if _, err := cameraAuth(strings.Repeat("x", 33)); err == nil {
		t.Fatal("accepted long access code")
	}
}
func listenTLS(t *testing.T, cfg *tls.Config) net.Listener {
	t.Helper()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	return listener
}
func TestCameraTLSStream(t *testing.T) {
	server, client, _ := testCertificates(t)
	listener := listenTLS(t, server)
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		auth := make([]byte, 80)
		if _, err := io.ReadFull(conn, auth); err != nil {
			done <- err
			return
		}
		expected, _ := cameraAuth("code")
		if !bytes.Equal(auth, expected) {
			done <- fmt.Errorf("wrong camera auth")
			return
		}
		_, err = conn.Write(framePacket([]byte{0xff, 0xd8, 0xff, 0xd9}))
		done <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	camera, err := DialCamera(ctx, CameraConfig{Address: listener.Addr().String(), AccessCode: "code", TLSConfig: client})
	if err != nil {
		t.Fatal(err)
	}
	defer camera.Close()
	if _, err := camera.ReadFrame(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestCameraCancellation(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	camera := &Camera{conn: a, timeout: time.Second, maxFrameSize: 1024}
	defer camera.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := camera.ReadFrame(ctx); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation blocked")
	}
}
func TestMQTTTLSRoundTrip(t *testing.T) {
	server, client, _ := testCertificates(t)
	listener := listenTLS(t, server)
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		packet, err := packets.ReadPacket(conn)
		if err != nil {
			done <- err
			return
		}
		connect, ok := packet.(*packets.ConnectPacket)
		if !ok || connect.Username != "bblp" || string(connect.Password) != "code" {
			done <- fmt.Errorf("wrong MQTT authentication")
			return
		}
		ack := packets.NewControlPacket(packets.Connack).(*packets.ConnackPacket)
		if err := ack.Write(conn); err != nil {
			done <- err
			return
		}
		packet, err = packets.ReadPacket(conn)
		if err != nil {
			done <- err
			return
		}
		sub, ok := packet.(*packets.SubscribePacket)
		if !ok || len(sub.Topics) != 1 || sub.Topics[0] != "device/SERIAL/report" {
			done <- fmt.Errorf("wrong MQTT topic")
			return
		}
		suback := packets.NewControlPacket(packets.Suback).(*packets.SubackPacket)
		suback.MessageID = sub.MessageID
		suback.ReturnCodes = []byte{0}
		if err := suback.Write(conn); err != nil {
			done <- err
			return
		}
		status := packets.NewControlPacket(packets.Publish).(*packets.PublishPacket)
		status.TopicName = "device/SERIAL/report"
		status.Payload = []byte(`{"print":{"command":"push_status","mc_percent":42}}`)
		if err := status.Write(conn); err != nil {
			done <- err
			return
		}
		packet, err = packets.ReadPacket(conn)
		if err != nil {
			done <- err
			return
		}
		publish, ok := packet.(*packets.PublishPacket)
		if !ok || publish.TopicName != "device/SERIAL/request" || publish.Retain || publish.Qos != 1 {
			done <- fmt.Errorf("wrong publish packet")
			return
		}
		puback := packets.NewControlPacket(packets.Puback).(*packets.PubackPacket)
		puback.MessageID = publish.MessageID
		if err := puback.Write(conn); err != nil {
			done <- err
			return
		}
		_, err = packets.ReadPacket(conn)
		if errors.Is(err, io.EOF) {
			err = nil
		}
		done <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, err := DialMQTT(ctx, MQTTConfig{Broker: "tls://" + listener.Addr().String(), DeviceID: "SERIAL", Username: "bblp", Password: "code", TLSConfig: client})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	select {
	case <-p.Reports():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	status, err := p.Status()
	if err != nil || status.Percent != 42 {
		t.Fatal(status, err)
	}
	if err := p.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	p.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestFTPSOperations(t *testing.T) {
	server, client, _ := testCertificates(t)
	listener := listenTLS(t, server)
	var mu sync.Mutex
	files := map[string][]byte{}
	var wg sync.WaitGroup
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				reader := bufio.NewReader(conn)
				reply := func(s string) { fmt.Fprint(conn, s+"\r\n") }
				reply("220 ready")
				var dataListener net.Listener
				defer func() {
					if dataListener != nil {
						dataListener.Close()
					}
				}()
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
					command, arg, _ := strings.Cut(line, " ")
					switch command {
					case "USER":
						if arg != "bblp" {
							t.Error("wrong FTP username")
						}
						reply("331 password")
					case "PASS":
						if arg != "code" {
							t.Error("wrong FTP password")
						}
						reply("230 logged in")
					case "FEAT":
						reply("211 no features")
					case "TYPE", "PBSZ", "PROT":
						reply("200 OK")
					case "EPSV":
						dataListener, err = tls.Listen("tcp", "127.0.0.1:0", server)
						if err != nil {
							t.Error(err)
							return
						}
						_, port, _ := net.SplitHostPort(dataListener.Addr().String())
						reply("229 Entering Extended Passive Mode (|||" + port + "|)")
					case "STOR", "RETR", "LIST":
						if dataListener == nil {
							t.Error("missing data connection")
							return
						}
						reply("150 transfer")
						dc, err := dataListener.Accept()
						dataListener.Close()
						dataListener = nil
						if err != nil {
							t.Error(err)
							return
						}
						dc.SetDeadline(time.Now().Add(5 * time.Second))
						if command == "STOR" {
							data, err := io.ReadAll(dc)
							if err != nil {
								t.Error(err)
							}
							mu.Lock()
							files[arg] = data
							mu.Unlock()
						} else if command == "RETR" {
							mu.Lock()
							data := append([]byte(nil), files[arg]...)
							mu.Unlock()
							if _, err := dc.Write(data); err != nil {
								t.Error(err)
							}
						} else {
							fmt.Fprint(dc, "-rw-r--r-- 1 bblp bblp 5 Jan 01 2026 test.3mf\r\n")
						}
						dc.Close()
						reply("226 complete")
					case "DELE":
						mu.Lock()
						delete(files, arg)
						mu.Unlock()
						reply("250 deleted")
					case "QUIT":
						reply("221 bye")
						return
					default:
						reply("500 unsupported")
					}
				}
			}()
		}
	}()
	f, err := NewFileClient(FileConfig{Address: listener.Addr().String(), AccessCode: "code", TLSConfig: client, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := f.Upload(ctx, "/test.3mf", strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := f.Download(ctx, "/test.3mf", &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "hello" {
		t.Fatal(output.String())
	}
	entries, err := f.List(ctx, "/")
	if err != nil || len(entries) != 1 || entries[0].Name != "test.3mf" {
		t.Fatal(entries, err)
	}
	if err := f.Upload(ctx, "/empty", strings.NewReader("")); err != nil {
		t.Fatal(err)
	}
	if err := f.Delete(ctx, "/test.3mf"); err != nil {
		t.Fatal(err)
	}
	if err := f.Delete(ctx, "bad\r\nQUIT"); err == nil {
		t.Fatal("accepted FTP command injection")
	}
	listener.Close()
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if _, ok := files["/test.3mf"]; ok {
		t.Fatal("file not deleted")
	}
	if len(files["/empty"]) != 0 {
		t.Fatal("empty upload failed")
	}
}
