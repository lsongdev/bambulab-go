// Package cli implements the bambulab command line interface.
package cli

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lsongdev/bambulab-go/bambulab"
)

const help = `Usage: bambulab [global flags] <command> [command flags / arguments]

Cloud:
  login --account EMAIL [--code CODE | --password-stdin]
  logout                     Remove locally saved credentials
  profile | preference | devices | status | projects
  tasks [--device ID] [--after CURSOR] [--limit N]
  messages [--type N] [--after CURSOR] [--limit N]
  project ID | setting ID | settings [VERSION] | version DEVICE_ID
  api METHOD /path [JSON]     Call any cloud API; use '-' for stdin JSON

Printer (use --device, plus --host/--access-code/--ca for LAN):
  watch                      Stream merged status as JSON lines until Ctrl-C
  pause | resume | stop | calibrate | unload
  speed 1..4 | light on|off | gcode CODE | print-gcode REMOTE_PATH
  print URL [--plate N] [--bed-level] [--flow-calibration] [--use-ams]
  command SECTION COMMAND [JSON]  Send an arbitrary MQTT command (QoS 0)

Files / camera (LAN only, --host, --access-code and --ca):
  files [REMOTE_DIR] | upload LOCAL REMOTE | download REMOTE LOCAL
  delete REMOTE | snapshot LOCAL.jpg

Global flags must appear before the command:
  --config PATH     Credentials file (default: user config dir/bambulab/credentials.json)
  --region REGION   china or global (default: saved region, then china)
  --token TOKEN     Access token; prefer BAMBU_ACCESS_TOKEN
  --api-url URL     Override HTTP API server (for testing/proxies)
  --timeout 30s     Request/operation timeout
  --device SERIAL  Printer serial number (BAMBU_DEVICE_ID)
  --host HOST      Printer LAN hostname/IP (BAMBU_HOST)
  --access-code C  LAN access code (BAMBU_ACCESS_CODE)
  --ca FILE        Trusted printer CA PEM (BAMBU_CA_FILE)

Login also accepts BAMBU_ACCOUNT, BAMBU_PASSWORD, BAMBU_CODE.
Output is JSON. Login never prints tokens. Control commands report MQTT delivery,
not successful printer execution. Cloud/firmware support varies by model.
`

type app struct {
	in                                                              io.Reader
	out, stderr                                                     io.Writer
	configPath, region, token, apiURL, device, host, accessCode, ca string
	timeout                                                         time.Duration
	cfg                                                             config
	client                                                          *bambulab.Client
}

func flags(name string, output io.Writer) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(output)
	return f
}
func (a *app) output(value any) error {
	encoder := json.NewEncoder(a.out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
func count(args []string, n int) error {
	if len(args) != n {
		return fmt.Errorf("expected %d argument(s); see 'bambulab help'", n)
	}
	return nil
}

func Run(ctx context.Context, args []string, in io.Reader, out, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		_, err := io.WriteString(out, help)
		return err
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	a := &app{in: in, out: out, stderr: stderr}
	f := flags("bambulab", stderr)
	f.StringVar(&a.configPath, "config", filepath.Join(dir, "bambulab", "credentials.json"), "credentials file")
	f.StringVar(&a.region, "region", os.Getenv("BAMBU_REGION"), "china or global")
	f.StringVar(&a.token, "token", os.Getenv("BAMBU_ACCESS_TOKEN"), "access token")
	f.StringVar(&a.apiURL, "api-url", os.Getenv("BAMBU_API_URL"), "API server")
	f.DurationVar(&a.timeout, "timeout", 30*time.Second, "operation timeout")
	f.StringVar(&a.device, "device", os.Getenv("BAMBU_DEVICE_ID"), "printer serial")
	f.StringVar(&a.host, "host", os.Getenv("BAMBU_HOST"), "printer LAN host")
	f.StringVar(&a.accessCode, "access-code", os.Getenv("BAMBU_ACCESS_CODE"), "LAN access code")
	f.StringVar(&a.ca, "ca", os.Getenv("BAMBU_CA_FILE"), "printer CA PEM")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	args = f.Args()
	if len(args) == 0 {
		return fmt.Errorf("command is required; see 'bambulab help'")
	}
	if args[0] == "help" {
		_, err := io.WriteString(out, help)
		return err
	}
	if a.timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	// Logout must work even if the saved JSON is damaged or has wrong permissions.
	if args[0] == "logout" {
		if err := count(args[1:], 0); err != nil {
			return err
		}
		if err := os.Remove(a.configPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return a.output(map[string]bool{"logged_out": true})
	}
	a.cfg, err = loadConfig(a.configPath)
	if err != nil {
		return err
	}
	if a.region == "" {
		a.region = string(a.cfg.Region)
	}
	if a.region == "" {
		a.region = "china"
	}
	if a.region != "china" && a.region != "global" {
		return fmt.Errorf("region must be china or global")
	}
	if a.token == "" && (a.cfg.Region == "" || string(a.cfg.Region) == a.region) {
		a.token = a.cfg.AccessToken
	}
	options := []bambulab.Option{bambulab.WithRegion(bambulab.Region(a.region)), bambulab.WithAccessToken(a.token), bambulab.WithHTTPClient(&http.Client{Timeout: a.timeout})}
	if a.apiURL != "" {
		options = append(options, bambulab.WithBaseURL(a.apiURL))
	}
	a.client = bambulab.NewClient(options...)
	if args[0] != "watch" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.timeout)
		defer cancel()
	}
	err = a.run(ctx, args[0], args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

func (a *app) run(ctx context.Context, command string, args []string) error {
	switch command {
	case "login":
		return a.login(ctx, args)
	case "profile", "preference", "devices", "status", "projects":
		if err := count(args, 0); err != nil {
			return err
		}
		if err := a.requireToken(); err != nil {
			return err
		}
		var result any
		var err error
		switch command {
		case "profile":
			result, err = a.client.GetProfileContext(ctx)
		case "preference":
			result, err = a.client.GetPreference(ctx)
		case "devices":
			result, err = a.client.ListDevicesContext(ctx)
		case "status":
			result, err = a.client.GetPrintStatus(ctx, true)
		case "projects":
			result, err = a.client.ListProjects(ctx)
		}
		if err != nil {
			return err
		}
		return a.output(result)
	case "tasks", "messages":
		f := flags(command, a.stderr)
		after := f.String("after", "", "pagination cursor")
		limit := f.Int("limit", 20, "page size")
		device := f.String("device", a.device, "device ID")
		typ := f.Int("type", -1, "message type")
		if err := f.Parse(args); err != nil {
			return err
		}
		if err := count(f.Args(), 0); err != nil {
			return err
		}
		if err := a.requireToken(); err != nil {
			return err
		}
		var result any
		var err error
		if command == "tasks" {
			result, err = a.client.ListTasks(ctx, bambulab.TaskQuery{DeviceID: *device, After: *after, Limit: *limit})
		} else {
			q := bambulab.MessageQuery{After: *after, Limit: *limit}
			if *typ >= 0 {
				q.Type = typ
			}
			result, err = a.client.ListMessages(ctx, q)
		}
		if err != nil {
			return err
		}
		return a.output(result)
	case "project", "setting", "settings", "version":
		if command == "settings" {
			if len(args) > 1 {
				return count(args, 1)
			}
		} else if err := count(args, 1); err != nil {
			return err
		}
		if err := a.requireToken(); err != nil {
			return err
		}
		var result any
		var err error
		switch command {
		case "project":
			result, err = a.client.GetProject(ctx, args[0])
		case "setting":
			result, err = a.client.GetSetting(ctx, args[0])
		case "version":
			result, err = a.client.GetDeviceVersion(ctx, args[0])
		case "settings":
			version := ""
			if len(args) > 0 {
				version = args[0]
			}
			result, err = a.client.ListSettings(ctx, version)
		}
		if err != nil {
			return err
		}
		return a.output(result)
	case "api":
		if len(args) < 2 || len(args) > 3 {
			return fmt.Errorf("usage: api METHOD /path [JSON]")
		}
		if err := a.requireToken(); err != nil {
			return err
		}
		var body any
		var err error
		if len(args) == 3 {
			body, err = a.jsonArg(args[2])
			if err != nil {
				return err
			}
		}
		var result json.RawMessage
		if err := a.client.Do(ctx, strings.ToUpper(args[0]), args[1], nil, body, &result); err != nil {
			return err
		}
		if len(result) == 0 {
			return a.output(map[string]bool{"success": true})
		}
		return a.output(result)
	case "files", "upload", "download", "delete", "snapshot":
		return a.files(ctx, command, args)
	case "watch", "pause", "resume", "stop", "calibrate", "unload", "speed", "light", "gcode", "print-gcode", "print", "command":
		return a.printer(ctx, command, args)
	default:
		return fmt.Errorf("unknown command %q; see 'bambulab help'", command)
	}
}
func (a *app) requireToken() error {
	if a.token == "" {
		return fmt.Errorf("login first or set BAMBU_ACCESS_TOKEN")
	}
	return nil
}
func (a *app) jsonArg(value string) (any, error) {
	if value == "-" {
		data, err := io.ReadAll(io.LimitReader(a.in, 1<<20+1))
		if err != nil {
			return nil, err
		}
		if len(data) > 1<<20 {
			return nil, fmt.Errorf("JSON exceeds 1 MiB")
		}
		value = string(data)
	}
	if !json.Valid([]byte(value)) {
		return nil, fmt.Errorf("invalid JSON")
	}
	return json.RawMessage(value), nil
}
func (a *app) login(ctx context.Context, args []string) error {
	f := flags("login", a.stderr)
	account := f.String("account", os.Getenv("BAMBU_ACCOUNT"), "account/email")
	code := f.String("code", os.Getenv("BAMBU_CODE"), "verification code")
	stdin := f.Bool("password-stdin", false, "read password from stdin")
	if err := f.Parse(args); err != nil {
		return err
	}
	if err := count(f.Args(), 0); err != nil {
		return err
	}
	password := os.Getenv("BAMBU_PASSWORD")
	if *code != "" {
		password = ""
	}
	if *stdin {
		if *code != "" {
			return fmt.Errorf("choose --code or --password-stdin")
		}
		data, err := io.ReadAll(io.LimitReader(a.in, 4097))
		if err != nil {
			return err
		}
		if len(data) > 4096 {
			return fmt.Errorf("password input too long")
		}
		password = strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	}
	response, err := a.client.LoginContext(ctx, &bambulab.Credential{Account: *account, Password: password, Code: *code})
	if err != nil {
		return err
	}
	if response.AccessToken == "" {
		if response.LoginType == "verifyCode" {
			return fmt.Errorf("verification required: rerun login --account %s --code CODE", *account)
		}
		return fmt.Errorf("login did not return an access token (loginType=%q)", response.LoginType)
	}
	if err := saveConfig(a.configPath, config{Region: bambulab.Region(a.region), AccessToken: response.AccessToken, RefreshToken: response.RefreshToken}); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}
	return a.output(map[string]any{"logged_in": true, "region": a.region, "expires_in": response.ExpiresIn, "config": a.configPath})
}
func (a *app) localTLS() (*tls.Config, error) {
	if a.host == "" || a.device == "" || a.accessCode == "" || a.ca == "" {
		return nil, fmt.Errorf("LAN access requires --host, --device, --access-code and --ca (or corresponding BAMBU_* variables)")
	}
	data, err := os.ReadFile(a.ca)
	if err != nil {
		return nil, err
	}
	return bambulab.PrinterTLSConfig(a.device, data)
}
func (a *app) dialPrinter(ctx context.Context) (*bambulab.Printer, error) {
	if a.device == "" {
		return nil, fmt.Errorf("--device SERIAL is required")
	}
	var cfg bambulab.MQTTConfig
	if a.host != "" {
		tlsConfig, err := a.localTLS()
		if err != nil {
			return nil, err
		}
		cfg = bambulab.LocalMQTTConfig(a.host, a.device, a.accessCode, tlsConfig)
	} else {
		if err := a.requireToken(); err != nil {
			return nil, err
		}
		pref, err := a.client.GetPreference(ctx)
		if err != nil {
			return nil, err
		}
		if pref.ID <= 0 {
			return nil, fmt.Errorf("preference response has no valid uid")
		}
		cfg = bambulab.CloudMQTTConfig(bambulab.Region(a.region), pref.ID, a.device, a.token)
	}
	cfg.Timeout = a.timeout
	return bambulab.DialMQTT(ctx, cfg)
}
func (a *app) printer(ctx context.Context, command string, args []string) error {
	var action func(*bambulab.Printer) error
	switch command {
	case "watch", "pause", "resume", "stop", "calibrate", "unload":
		if err := count(args, 0); err != nil {
			return err
		}
		action = func(p *bambulab.Printer) error {
			switch command {
			case "watch":
				return p.PushAll(ctx)
			case "pause":
				return p.Pause(ctx)
			case "resume":
				return p.Resume(ctx)
			case "stop":
				return p.Stop(ctx)
			case "calibrate":
				return p.Calibrate(ctx)
			default:
				return p.UnloadFilament(ctx)
			}
		}
	case "speed", "light", "gcode", "print-gcode":
		if err := count(args, 1); err != nil {
			return err
		}
		switch command {
		case "speed":
			speed, err := strconv.Atoi(args[0])
			if err != nil || speed < 1 || speed > 4 {
				return fmt.Errorf("speed must be 1..4")
			}
			action = func(p *bambulab.Printer) error { return p.SetPrintSpeed(ctx, bambulab.PrintSpeed(speed)) }
		case "light":
			if args[0] != "on" && args[0] != "off" {
				return fmt.Errorf("light must be on or off")
			}
			action = func(p *bambulab.Printer) error { return p.SetLight(ctx, args[0] == "on") }
		case "gcode":
			action = func(p *bambulab.Printer) error { return p.SendGCode(ctx, args[0]) }
		case "print-gcode":
			action = func(p *bambulab.Printer) error { return p.PrintGCodeFile(ctx, args[0]) }
		}
	case "print":
		if len(args) == 0 {
			return fmt.Errorf("usage: print URL [--plate N] [--bed-level] [--flow-calibration] [--use-ams]")
		}
		f := flags("print", a.stderr)
		plate := f.Int("plate", 1, "plate index")
		bed := f.Bool("bed-level", false, "level bed")
		flow := f.Bool("flow-calibration", false, "calibrate flow")
		ams := f.Bool("use-ams", false, "use AMS")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if err := count(f.Args(), 0); err != nil {
			return err
		}
		if *plate < 1 {
			return fmt.Errorf("plate must be positive")
		}
		file := bambulab.LocalProjectFile(args[0], *plate)
		file.BedLevelling = *bed
		file.FlowCalibration = *flow
		file.UseAMS = *ams
		action = func(p *bambulab.Printer) error { return p.PrintProject(ctx, file) }
	case "command":
		if len(args) < 2 || len(args) > 3 {
			return fmt.Errorf("usage: command SECTION COMMAND [JSON]")
		}
		var fields any
		var err error
		if len(args) == 3 {
			fields, err = a.jsonArg(args[2])
			if err != nil {
				return err
			}
		}
		action = func(p *bambulab.Printer) error { _, err := p.Send(ctx, args[0], args[1], fields, 0); return err }
	}
	p, err := a.dialPrinter(ctx)
	if err != nil {
		return err
	}
	defer p.Close()
	if err := action(p); err != nil {
		return err
	}
	if command != "watch" {
		return a.output(map[string]any{"sent": true, "command": command, "device": a.device})
	}
	encoder := json.NewEncoder(a.out)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-p.Done():
			select {
			case err := <-p.Errors():
				return err
			default:
				return bambulab.ErrClosed
			}
		case err := <-p.Errors():
			return err
		case report := <-p.Reports():
			if _, ok := report.Payload["print"]; ok {
				if err := encoder.Encode(p.Snapshot()); err != nil {
					return err
				}
			}
		}
	}
}
func (a *app) files(ctx context.Context, command string, args []string) error {
	switch command {
	case "files":
		if len(args) == 0 {
			args = []string{"/"}
		}
		if err := count(args, 1); err != nil {
			return err
		}
	case "upload", "download":
		if err := count(args, 2); err != nil {
			return err
		}
	default:
		if err := count(args, 1); err != nil {
			return err
		}
	}
	tlsConfig, err := a.localTLS()
	if err != nil {
		return err
	}
	if command == "snapshot" {
		camera, err := bambulab.DialCamera(ctx, bambulab.CameraConfig{Address: net.JoinHostPort(a.host, "6000"), AccessCode: a.accessCode, TLSConfig: tlsConfig, Timeout: a.timeout})
		if err != nil {
			return err
		}
		defer camera.Close()
		frame, err := camera.ReadFrame(ctx)
		if err != nil {
			return err
		}
		if err := writeNewFile(args[0], func(w io.Writer) error { _, err := w.Write(frame); return err }); err != nil {
			return err
		}
		return a.output(map[string]string{"saved": args[0]})
	}
	client, err := bambulab.NewFileClient(bambulab.FileConfig{Address: net.JoinHostPort(a.host, "990"), AccessCode: a.accessCode, TLSConfig: tlsConfig, Timeout: a.timeout})
	if err != nil {
		return err
	}
	switch command {
	case "files":
		entries, err := client.List(ctx, args[0])
		if err != nil {
			return err
		}
		return a.output(entries)
	case "upload":
		f, err := os.Open(args[0])
		if err != nil {
			return err
		}
		defer f.Close()
		if err := client.Upload(ctx, args[1], f); err != nil {
			return err
		}
	case "download":
		if err := writeNewFile(args[1], func(w io.Writer) error { return client.Download(ctx, args[0], w) }); err != nil {
			return err
		}
	case "delete":
		if err := client.Delete(ctx, args[0]); err != nil {
			return err
		}
	}
	return a.output(map[string]bool{"success": true})
}
func writeNewFile(path string, write func(io.Writer) error) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if err := write(f); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}
