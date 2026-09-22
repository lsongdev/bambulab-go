package bambulab

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestStatusMergeAndIsolation(t *testing.T) {
	p := newPrinter("serial", time.Second, 1)
	p.receive([]byte(`{"print":{"command":"push_status","mc_percent":40,"nozzle_temper":210,"ams":{"tray_now":"1","tray_tar":"2"},"hms":[{"code":1}]}}`))
	p.receive([]byte(`{"print":{"command":"push_status","mc_percent":0,"ams":{"tray_now":"0"},"hms":[]}}`))
	s, err := p.Status()
	if err != nil {
		t.Fatal(err)
	}
	if s.Percent != 0 || s.NozzleTemperature != 210 || s.AMS.CurrentTray != "0" || s.AMS.TargetTray != "2" || len(s.HMS) != 0 {
		t.Fatalf("%+v", s)
	}
	if p.DroppedReports() != 1 {
		t.Fatal("expected dropped report")
	}
	raw := p.Snapshot()
	raw["nozzle_temper"][0] = '9'
	delete(raw, "ams")
	p.receive([]byte(`{"print":{"command":"pause","result":"success","mc_percent":99}}`))
	s, _ = p.Status()
	if s.Percent != 0 || s.NozzleTemperature != 210 {
		t.Fatalf("snapshot aliases state: %+v", s)
	}
	p.receive([]byte(`not json`))
	select {
	case <-p.Errors():
	default:
		t.Fatal("missing parse error")
	}
}
func TestCommands(t *testing.T) {
	p := newPrinter("serial", time.Second, 1)
	var payload Object
	var qos byte
	p.publish = func(_ context.Context, topic string, q byte, data []byte) error {
		if topic != "device/serial/request" {
			t.Fatal(topic)
		}
		qos = q
		return json.Unmarshal(data, &payload)
	}
	ctx := context.Background()
	for _, command := range []func(context.Context) error{p.Pause, p.Resume, p.Stop} {
		if err := command(ctx); err != nil {
			t.Fatal(err)
		}
		if qos != 1 {
			t.Fatal("control must use QoS 1")
		}
	}
	if err := p.SetPrintSpeed(ctx, SpeedSport); err != nil {
		t.Fatal(err)
	}
	var print map[string]any
	json.Unmarshal(payload["print"], &print)
	if print["param"] != "3" || print["sequence_id"] != "4" {
		t.Fatal(print)
	}
	if err := p.SetLight(ctx, false); err != nil {
		t.Fatal(err)
	}
	var led map[string]any
	json.Unmarshal(payload["system"], &led)
	for _, key := range []string{"led_on_time", "led_off_time", "loop_times", "interval_time"} {
		if _, ok := led[key]; !ok {
			t.Fatal(key)
		}
	}
	if err := p.SetPrintSpeed(ctx, 5); err == nil {
		t.Fatal("invalid speed")
	}
	if err := p.SetAMSFilament(ctx, AMSFilamentSetting{Color: "notcolor"}); err == nil {
		t.Fatal("invalid color")
	}
	_, err := p.Send(ctx, "print", "test", Object{"large": json.RawMessage(`9007199254740993`)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var object Object
	json.Unmarshal(payload["print"], &object)
	if string(object["large"]) != "9007199254740993" {
		t.Fatal(string(object["large"]))
	}
	p.Close()
	if err := p.Pause(ctx); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}
func TestRequestCorrelation(t *testing.T) {
	for _, result := range []string{"SUCCESS", "failed"} {
		t.Run(result, func(t *testing.T) {
			p := newPrinter("serial", time.Second, 4)
			p.publish = func(_ context.Context, _ string, _ byte, data []byte) error {
				var request map[string]map[string]any
				json.Unmarshal(data, &request)
				p.receive([]byte(`{"info":{"command":"other","sequence_id":"1","result":"failed"}}`))
				request["info"]["result"] = result
				raw, _ := json.Marshal(request)
				p.receive(raw)
				return nil
			}
			_, err := p.Request(context.Background(), "info", "get_version", nil, 0)
			if result == "SUCCESS" && err != nil {
				t.Fatal(err)
			}
			var commandErr *CommandError
			if result == "failed" && !errors.As(err, &commandErr) {
				t.Fatal(err)
			}
			if len(p.pending) != 0 {
				t.Fatal("pending request leaked")
			}
		})
	}
	p := newPrinter("serial", time.Millisecond, 1)
	p.publish = func(context.Context, string, byte, []byte) error { return nil }
	if _, err := p.Request(context.Background(), "info", "get_version", nil, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
func TestConcurrentSequences(t *testing.T) {
	p := newPrinter("serial", time.Second, 1)
	var mu sync.Mutex
	seen := map[string]bool{}
	p.publish = func(_ context.Context, _ string, _ byte, data []byte) error {
		var body map[string]map[string]string
		if err := json.Unmarshal(data, &body); err != nil {
			return err
		}
		seq := body["print"]["sequence_id"]
		mu.Lock()
		defer mu.Unlock()
		if seen[seq] {
			t.Errorf("duplicate sequence %s", seq)
		}
		seen[seq] = true
		return nil
	}
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.Pause(context.Background()); err != nil {
				t.Error(err)
			}
			p.Snapshot()
		}()
	}
	wg.Wait()
	if len(seen) != 100 {
		t.Fatal(len(seen))
	}
}
