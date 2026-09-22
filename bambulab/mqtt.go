package bambulab

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

const (
	MQTTChina  = "tls://cn.mqtt.bambulab.com:8883"
	MQTTGlobal = "tls://us.mqtt.bambulab.com:8883"
)

var ErrClosed = errors.New("bambulab: printer connection is closed")

type MQTTConfig struct {
	Broker     string
	DeviceID   string
	Username   string
	Password   string
	ClientID   string
	TLSConfig  *tls.Config
	Timeout    time.Duration // defaults to 15 seconds
	BufferSize int           // defaults to 64; excess reports are dropped, but state is merged
}

func LocalMQTTConfig(host, serial, accessCode string, tlsConfig *tls.Config) MQTTConfig {
	return MQTTConfig{Broker: "tls://" + net.JoinHostPort(host, "8883"), DeviceID: serial, Username: "bblp", Password: strings.TrimSpace(accessCode), TLSConfig: tlsConfig}
}
func CloudMQTTConfig(region Region, userID int64, deviceID, token string) MQTTConfig {
	broker := MQTTChina
	if region == RegionGlobal {
		broker = MQTTGlobal
	}
	return MQTTConfig{Broker: broker, DeviceID: deviceID, Username: "u_" + strconv.FormatInt(userID, 10), Password: token}
}

// Report contains the unmodified JSON sections received from a printer.
type Report struct {
	DeviceID string
	Payload  Object
}
type CommandError struct{ Section, Command, SequenceID, Result, Reason string }

func (e *CommandError) Error() string {
	return fmt.Sprintf("bambulab: %s.%s (%s): %s %s", e.Section, e.Command, e.SequenceID, e.Result, e.Reason)
}

// Printer manages one device. Use Done to detect closure. Reports and Errors are
// bounded channels and are not closed; snapshots still update when reports drop.
// Automatic reconnect and command replay are disabled. Dial again after loss.
type Printer struct {
	client   mqtt.Client
	deviceID string
	timeout  time.Duration
	seq      atomic.Uint64
	dropped  atomic.Uint64
	mu       sync.RWMutex
	state    Object
	pending  map[string]chan json.RawMessage
	reports  chan Report
	errors   chan error
	done     chan struct{}
	closed   bool
	publish  func(context.Context, string, byte, []byte) error
}

func newPrinter(deviceID string, timeout time.Duration, buffer int) *Printer {
	return &Printer{deviceID: deviceID, timeout: timeout, state: Object{}, pending: map[string]chan json.RawMessage{}, reports: make(chan Report, buffer), errors: make(chan error, buffer), done: make(chan struct{})}
}

func DialMQTT(ctx context.Context, config MQTTConfig) (*Printer, error) {
	if config.DeviceID == "" || strings.ContainsAny(config.DeviceID, "/+#\x00") || config.Username == "" || config.Password == "" {
		return nil, fmt.Errorf("bambulab: device ID and MQTT credentials are required")
	}
	u, err := url.Parse(config.Broker)
	if err != nil || (u.Scheme != "tls" && u.Scheme != "ssl") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return nil, fmt.Errorf("bambulab: MQTT broker must be a TLS URL")
	}
	if config.Timeout == 0 {
		config.Timeout = 15 * time.Second
	}
	if config.BufferSize == 0 {
		config.BufferSize = 64
	}
	if config.Timeout < 0 || config.BufferSize < 0 {
		return nil, fmt.Errorf("bambulab: timeout and buffer size must be positive")
	}
	if config.ClientID == "" {
		var id [8]byte
		if _, err := rand.Read(id[:]); err != nil {
			return nil, err
		}
		config.ClientID = "bambu-go-" + hex.EncodeToString(id[:])
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if config.TLSConfig != nil {
		tlsConfig = config.TLSConfig.Clone()
	}
	p := newPrinter(config.DeviceID, config.Timeout, config.BufferSize)
	opts := mqtt.NewClientOptions().AddBroker(config.Broker).SetClientID(config.ClientID).
		SetUsername(config.Username).SetPassword(config.Password).SetTLSConfig(tlsConfig).
		SetConnectTimeout(config.Timeout).SetWriteTimeout(config.Timeout).
		SetAutoReconnect(false).SetConnectRetry(false).SetCleanSession(true).
		SetConnectionLostHandler(func(_ mqtt.Client, err error) { p.fail(err) })
	p.client = mqtt.NewClient(opts)
	connectCtx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	token := p.client.Connect()
	if err := waitToken(connectCtx, token); err != nil {
		p.fail(err)
		// Connect itself is bounded; clean up if it completes after cancellation.
		go func() {
			<-token.Done()
			if token.Error() == nil {
				p.client.Disconnect(0)
			}
		}()
		return nil, err
	}
	p.publish = func(ctx context.Context, topic string, qos byte, payload []byte) error {
		return waitToken(ctx, p.client.Publish(topic, qos, false, payload))
	}
	if err := waitToken(connectCtx, p.client.Subscribe("device/"+config.DeviceID+"/report", 0, func(_ mqtt.Client, msg mqtt.Message) { p.receive(msg.Payload()) })); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

func waitToken(ctx context.Context, token mqtt.Token) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-token.Done():
		return token.Error()
	}
}
func (p *Printer) Reports() <-chan Report { return p.reports }
func (p *Printer) Errors() <-chan error   { return p.errors }
func (p *Printer) Done() <-chan struct{}  { return p.done }
func (p *Printer) DroppedReports() uint64 { return p.dropped.Load() }
func (p *Printer) fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	if err != nil {
		select {
		case p.errors <- err:
		default:
		}
	}
	p.closed = true
	close(p.done)
}
func (p *Printer) Close() error {
	p.fail(nil)
	if p.client != nil {
		p.client.Disconnect(0)
	}
	return nil
}

func (p *Printer) receive(data []byte) {
	var report Object
	if err := json.Unmarshal(data, &report); err != nil || report == nil {
		select {
		case p.errors <- fmt.Errorf("bambulab: malformed MQTT report"):
		default:
		}
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	for section, raw := range report {
		var meta struct {
			SequenceID json.RawMessage `json:"sequence_id"`
			Command    string          `json:"command"`
		}
		if json.Unmarshal(raw, &meta) != nil {
			continue
		}
		var seq string
		if len(meta.SequenceID) > 0 {
			if json.Unmarshal(meta.SequenceID, &seq) != nil {
				seq = string(meta.SequenceID)
			}
		}
		if pending := p.pending[section+":"+meta.Command+":"+seq]; pending != nil {
			select {
			case pending <- append(json.RawMessage(nil), raw...):
			default:
			}
		}
		if section == "print" && meta.Command == "push_status" {
			var delta Object
			if json.Unmarshal(raw, &delta) == nil {
				mergeObject(p.state, delta)
			}
		}
	}
	select {
	case p.reports <- Report{DeviceID: p.deviceID, Payload: report}:
	default:
		p.dropped.Add(1)
	}
}

func mergeObject(dst, patch Object) {
	for key, value := range patch {
		var next, old Object
		if json.Unmarshal(value, &next) == nil && next != nil && json.Unmarshal(dst[key], &old) == nil && old != nil {
			mergeObject(old, next)
			dst[key], _ = json.Marshal(old)
		} else {
			dst[key] = append(json.RawMessage(nil), value...)
		}
	}
}

// Snapshot returns an independent copy of the merged print status, including
// unknown firmware fields. Arrays replace prior arrays; objects merge recursively.
func (p *Printer) Snapshot() Object {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := Object{}
	for k, v := range p.state {
		result[k] = append(json.RawMessage(nil), v...)
	}
	return result
}

func (p *Printer) encode(section, command string, fields any) (string, []byte, error) {
	if section == "" || command == "" {
		return "", nil, fmt.Errorf("bambulab: section and command are required")
	}
	body := Object{}
	if fields != nil {
		data, err := json.Marshal(fields)
		if err != nil {
			return "", nil, err
		}
		if err := json.Unmarshal(data, &body); err != nil || body == nil {
			return "", nil, fmt.Errorf("bambulab: command fields must be a JSON object")
		}
	}
	seq := strconv.FormatUint(p.seq.Add(1), 10)
	body["sequence_id"], _ = json.Marshal(seq)
	body["command"], _ = json.Marshal(command)
	data, err := json.Marshal(map[string]any{section: body})
	return seq, data, err
}
func (p *Printer) send(ctx context.Context, qos byte, data []byte) error {
	if qos > 1 {
		return fmt.Errorf("bambulab: QoS must be 0 or 1")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-p.done:
		return ErrClosed
	default:
	}
	return p.publish(ctx, "device/"+p.deviceID+"/request", qos, data)
}

// Send waits for MQTT delivery, not printer execution. Cancellation cannot undo a
// command already transmitted. Commands are never retained or replayed by the SDK.
func (p *Printer) Send(ctx context.Context, section, command string, fields any, qos byte) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	seq, data, err := p.encode(section, command, fields)
	if err != nil {
		return "", err
	}
	return seq, p.send(ctx, qos, data)
}

// Request additionally waits for a report matching section, command and sequence.
// Some commands (notably pushing.pushall) respond in another section; use Send for them.
func (p *Printer) Request(ctx context.Context, section, command string, fields any, qos byte) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	seq, data, err := p.encode(section, command, fields)
	if err != nil {
		return nil, err
	}
	key := section + ":" + command + ":" + seq
	ch := make(chan json.RawMessage, 1)
	p.mu.Lock()
	p.pending[key] = ch
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, key); p.mu.Unlock() }()
	if err := p.send(ctx, qos, data); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.done:
		return nil, ErrClosed
	case raw := <-ch:
		var result struct {
			Result string `json:"result"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, err
		}
		if result.Result != "" && !strings.EqualFold(result.Result, "success") {
			return raw, &CommandError{Section: section, Command: command, SequenceID: seq, Result: result.Result, Reason: result.Reason}
		}
		return raw, nil
	}
}
