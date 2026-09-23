// Package cli implements the bambulab command line interface.
package cli

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
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
	"text/tabwriter"
	"time"

	"github.com/lsongdev/bambulab-go/bambulab"
	"golang.org/x/term"
)

const help = `Usage: bambulab [global flags] <command> [command flags / arguments]

Cloud:
  login                      Prompt for account, password, and verification code
  login --account EMAIL [--code CODE | --password-stdin]
  logout                     Remove locally saved credentials
  devices                    List bound printers with discovered LAN IPs (cached)
  status                     Show selected printer's cloud print-job summary
  version                    Show selected printer's firmware version
  profile | preference | projects
  tasks [--device ID] [--after CURSOR] [--limit N]
  messages [--type N] [--after CURSOR] [--limit N]
  project ID | setting ID | settings [VERSION]
  api METHOD /path [JSON]     Call any cloud API; use '-' for stdin JSON

Printer (defaults to first bound printer; use --device to override):
  watch                      Stream merged status as JSON lines until Ctrl-C
  state                      Show one live status report (first printer by default)
  pause | resume | stop | calibrate | unload
  speed 1..4 | light on|off | gcode CODE | print-gcode REMOTE_PATH
  print URL [--plate N] [--bed-level] [--flow-calibration] [--use-ams]
  command SECTION COMMAND [JSON]  Send an arbitrary MQTT command (QoS 0)

Files / camera (discover --host and --access-code when logged in):
  ls [REMOTE_DIR] | files [REMOTE_DIR] | upload LOCAL REMOTE | download REMOTE LOCAL
  rm REMOTE | snapshot [LOCAL.jpg]

Global flags must appear before the command, except --device and --json:
  --json            Output JSON for device listings and printer details
  --config PATH     Credentials file (default: user config dir/bambulab/credentials.json)
  --region REGION   china or global (default: saved region, then china)
  --token TOKEN     Access token; prefer BAMBU_ACCESS_TOKEN
  --api-url URL     Override HTTP API server (for testing/proxies)
  --timeout 30s     Request/operation timeout
  --device SERIAL  Printer serial number (default: first bound printer)
  --host HOST      Printer LAN hostname/IP (BAMBU_HOST)
  --access-code C  LAN access code (BAMBU_ACCESS_CODE)
  --ca FILE        Override bundled printer CA PEM (BAMBU_CA_FILE)

Login also accepts BAMBU_ACCOUNT, BAMBU_PASSWORD, BAMBU_CODE.
Device listings are readable text by default; --json outputs the devices array with discovered IPs.
File commands use readable text by default and JSON with --json. Login never prints tokens.
Control commands report MQTT delivery,
not successful printer execution. Cloud/firmware support varies by model.
`

type app struct {
	in                                                              io.Reader
	out, stderr                                                     io.Writer
	configPath, region, token, apiURL, device, host, accessCode, ca string
	timeout                                                         time.Duration
	cfg                                                             config
	client                                                          *bambulab.Client
	jsonOutput                                                      bool
	hostFromCache                                                   bool
}

type deviceInfo struct {
	bambulab.Device
	IP string `json:"ip,omitempty"`
}

var discoverPrinters = bambulab.DiscoverPrinters
var discoverPrinter = bambulab.DiscoverPrinter

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
	args, jsonOutput := extractJSONFlag(args)
	var selectedDevice string
	var deviceSpecified bool
	var err error
	args, selectedDevice, deviceSpecified, err = extractDeviceFlag(args)
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		_, err := io.WriteString(out, help)
		return err
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	a := &app{in: in, out: out, stderr: stderr, jsonOutput: jsonOutput}
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
	if deviceSpecified {
		a.device = selectedDevice
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
		if err := a.clearDeviceCache(); err != nil {
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
	if args[0] != "watch" && args[0] != "login" {
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

func extractJSONFlag(args []string) ([]string, bool) {
	filtered := make([]string, 0, len(args))
	jsonOutput := false
	for _, arg := range args {
		if arg == "--json" {
			jsonOutput = true
		} else {
			filtered = append(filtered, arg)
		}
	}
	return filtered, jsonOutput
}

func extractDeviceFlag(args []string) ([]string, string, bool, error) {
	filtered := make([]string, 0, len(args))
	var device string
	found := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--device" {
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return nil, "", false, fmt.Errorf("--device requires a printer serial")
			}
			i++
			device, found = args[i], true
		} else if strings.HasPrefix(arg, "--device=") {
			device, found = strings.TrimPrefix(arg, "--device="), true
			if device == "" {
				return nil, "", false, fmt.Errorf("--device requires a printer serial")
			}
		} else {
			filtered = append(filtered, arg)
		}
	}
	return filtered, device, found, nil
}

func (a *app) run(ctx context.Context, command string, args []string) error {
	switch command {
	case "login":
		return a.login(ctx, args)
	case "devices":
		if err := count(args, 0); err != nil {
			return err
		}
		devices, err := a.listDevices(ctx, true)
		if err != nil {
			return err
		}
		if a.jsonOutput {
			return a.output(devices)
		}
		return a.outputDeviceList(devices)
	case "status":
		return a.printerStatus(ctx, args)
	case "profile", "preference", "projects":
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
		} else if command == "version" {
			if err := count(args, 0); err != nil {
				return err
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
			if err = a.resolveDevice(ctx); err != nil {
				return err
			}
			result, err = a.client.GetDeviceVersion(ctx, a.device)
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
	case "ls", "files", "upload", "download", "rm", "snapshot":
		return a.files(ctx, command, args)
	case "state":
		if err := count(args, 0); err != nil {
			return err
		}
		return a.state(ctx)
	case "watch", "pause", "resume", "stop", "calibrate", "unload", "speed", "light", "gcode", "print-gcode", "print", "command":
		return a.printer(ctx, command, args)
	default:
		return fmt.Errorf("unknown command %q; see 'bambulab help'", command)
	}
}

type printerStatus struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Online     bool    `json:"online"`
	Model      string  `json:"model"`
	Product    string  `json:"product"`
	TaskID     *string `json:"task_id"`
	TaskName   *string `json:"task_name"`
	TaskStatus *string `json:"task_status"`
	Progress   *int    `json:"progress"`
	Prediction *int    `json:"prediction"`
}

func (a *app) printerStatus(ctx context.Context, args []string) error {
	if err := count(args, 0); err != nil {
		return err
	}
	if err := a.requireToken(); err != nil {
		return err
	}
	if err := a.resolveDevice(ctx); err != nil {
		return err
	}
	result, err := a.client.GetPrintStatus(ctx, true)
	if err != nil {
		return err
	}
	for _, device := range result.Devices {
		if device.ID == a.device {
			if !a.jsonOutput {
				return a.outputPrinterStatus(device)
			}
			return a.output(printerStatus{ID: device.ID, Name: device.Name, Online: device.Online, Model: device.Model, Product: device.Product, TaskID: device.TaskID, TaskName: device.TaskName, TaskStatus: device.TaskStatus, Progress: device.Progress, Prediction: device.Prediction})
		}
	}
	return fmt.Errorf("printer %q not found", a.device)
}

func (a *app) outputDeviceList(devices []deviceInfo) error {
	w := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "NAME\tSERIAL\tIP\tMODEL\tONLINE\tPRINT\tACCESS CODE"); err != nil {
		return err
	}
	for _, device := range devices {
		name := device.Name
		if name == "" {
			name = device.ID
		}
		model := device.Product
		if model == "" {
			model = device.Model
		}
		online := "no"
		if device.Online {
			online = "yes"
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", name, device.ID, device.IP, model, online, device.PrintStatus, strings.TrimSpace(device.AccessCode)); err != nil {
			return err
		}
	}
	return w.Flush()
}

func (a *app) outputPrinterStatus(device bambulab.DevicePrintStatus) error {
	online := "offline"
	if device.Online {
		online = "online"
	}
	name := device.Name
	if name == "" {
		name = device.ID
	}
	if _, err := fmt.Fprintf(a.out, "%s\nSerial: %s\nState: %s\n", name, device.ID, online); err != nil {
		return err
	}
	if device.TaskName != nil {
		if _, err := fmt.Fprintf(a.out, "Task: %s\n", *device.TaskName); err != nil {
			return err
		}
	}
	if device.TaskStatus != nil {
		if _, err := fmt.Fprintf(a.out, "Task status: %s\n", *device.TaskStatus); err != nil {
			return err
		}
	}
	if device.Progress != nil {
		if _, err := fmt.Fprintf(a.out, "Progress: %d%%\n", *device.Progress); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) listDevices(ctx context.Context, withIP bool) ([]deviceInfo, error) {
	bound, _, err := a.boundDevices(ctx)
	if err != nil {
		return nil, err
	}
	devices := make([]deviceInfo, len(bound))
	for i, device := range bound {
		devices[i].Device = device
	}
	if !withIP || len(devices) == 0 {
		return devices, nil
	}
	missing := make([]string, 0, len(devices))
	for i := range devices {
		devices[i].IP = a.cachedPrinterIP(devices[i].ID)
		if devices[i].IP == "" && devices[i].ID != "" {
			missing = append(missing, devices[i].ID)
		}
	}
	if len(missing) == 0 {
		return devices, nil
	}
	var announced []bambulab.DiscoveredPrinter
	if len(missing) == 1 {
		printer, discoverErr := discoverPrinter(ctx, missing[0])
		if discoverErr == nil {
			announced = []bambulab.DiscoveredPrinter{printer}
		}
		err = discoverErr
	} else {
		announced, err = discoverPrinters(ctx, 0)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return devices, nil
	}
	byID := make(map[string]string, len(announced))
	for _, printer := range announced {
		if printer.IP != nil {
			byID[printer.ID] = printer.IP.String()
		}
	}
	for i := range devices {
		if ip := byID[devices[i].ID]; ip != "" {
			devices[i].IP = ip
			a.rememberPrinterIP(devices[i].ID, ip)
		}
	}
	return devices, nil
}

func (a *app) resolveDevice(ctx context.Context) error {
	return a.selectDevice(ctx, false)
}

func (a *app) selectDevice(ctx context.Context, needAccessCode bool) error {
	if a.device != "" && (!needAccessCode || a.accessCode != "") {
		return nil
	}
	devices, err := a.listDevices(ctx, false)
	if err != nil {
		return err
	}
	if a.device == "" {
		if len(devices) == 0 {
			return fmt.Errorf("no printers bound to account")
		}
		a.device = devices[0].ID
		if a.device == "" {
			return fmt.Errorf("first bound printer has no serial number")
		}
	}
	if needAccessCode && a.accessCode == "" {
		for _, device := range devices {
			if device.ID == a.device {
				a.accessCode = strings.TrimSpace(device.AccessCode)
				break
			}
		}
		if a.accessCode == "" {
			return fmt.Errorf("no access code available for printer %s; use --access-code", a.device)
		}
	}
	return nil
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
	interactive := !*stdin && password == "" && *code == ""
	if interactive {
		reader := bufio.NewReader(a.in)
		if *account == "" {
			var err error
			*account, err = a.prompt(ctx, reader, "Account/Email: ", false)
			if err != nil {
				return err
			}
		}
		var err error
		password, err = a.prompt(ctx, reader, "Password: ", true)
		if err != nil {
			return err
		}
		for {
			response, err := a.client.LoginContext(ctx, &bambulab.Credential{Account: *account, Password: password, Code: *code})
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err == nil && response.AccessToken != "" {
				return a.saveLogin(response)
			}
			if err == nil && response.LoginType == "verifyCode" {
				*code, err = a.prompt(ctx, reader, "Verification code: ", false)
				if err != nil {
					return err
				}
				password = ""
				continue
			}
			if err == nil {
				err = fmt.Errorf("login did not return an access token (loginType=%q)", response.LoginType)
			}
			if _, writeErr := fmt.Fprintf(a.stderr, "Login failed: %v\n", err); writeErr != nil {
				return writeErr
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if *code != "" {
				*code, err = a.prompt(ctx, reader, "Verification code: ", false)
			} else {
				*account, err = a.prompt(ctx, reader, "Account/Email: ", false)
				if err == nil {
					password, err = a.prompt(ctx, reader, "Password: ", true)
				}
			}
			if err != nil {
				return err
			}
		}
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
	return a.saveLogin(response)
}

func (a *app) saveLogin(response *bambulab.LoginResponse) error {
	if err := saveConfig(a.configPath, config{Region: bambulab.Region(a.region), AccessToken: response.AccessToken, RefreshToken: response.RefreshToken}); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}
	return a.output(map[string]any{"logged_in": true, "region": a.region, "expires_in": response.ExpiresIn, "config": a.configPath})
}

func (a *app) prompt(ctx context.Context, reader *bufio.Reader, label string, secret bool) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if _, err := io.WriteString(a.stderr, label); err != nil {
			return "", err
		}
		read := func() (string, error) {
			if input, ok := a.in.(*os.File); ok && input == os.Stdin && term.IsTerminal(int(input.Fd())) && secret {
				data, err := term.ReadPassword(int(input.Fd()))
				if _, writeErr := io.WriteString(a.stderr, "\n"); writeErr != nil {
					return "", writeErr
				}
				return string(data), err
			}
			return readPromptLine(reader)
		}
		var value string
		var err error
		if input, ok := a.in.(*os.File); ok && input == os.Stdin {
			type result struct {
				value string
				err   error
			}
			resultCh := make(chan result, 1)
			var state *term.State
			if secret && term.IsTerminal(int(input.Fd())) {
				state, _ = term.GetState(int(input.Fd()))
			}
			go func() {
				v, e := read()
				resultCh <- result{v, e}
			}()
			select {
			case got := <-resultCh:
				value, err = got.value, got.err
			case <-ctx.Done():
				if state != nil {
					_ = term.Restore(int(input.Fd()), state)
				}
				return "", ctx.Err()
			}
		} else {
			value, err = read()
		}
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(value) != "" {
			return value, nil
		}
		if _, err := io.WriteString(a.stderr, "A value is required.\n"); err != nil {
			return "", err
		}
	}
}

func readPromptLine(reader *bufio.Reader) (string, error) {
	var data []byte
	for {
		part, err := reader.ReadSlice('\n')
		data = append(data, part...)
		if len(data) > 4096 {
			return "", fmt.Errorf("input too long")
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && (!errors.Is(err, io.EOF) || len(data) == 0) {
			return "", err
		}
		return strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r"), nil
	}
}
func (a *app) localTLS() (*tls.Config, error) {
	if a.host == "" || a.device == "" || a.accessCode == "" {
		return nil, fmt.Errorf("LAN access requires --host and --access-code (or corresponding BAMBU_* variables); select a printer with --device or log in")
	}
	if a.ca == "" {
		return bambulab.DefaultPrinterTLSConfig(a.device)
	}
	data, err := os.ReadFile(a.ca)
	if err != nil {
		return nil, err
	}
	return bambulab.PrinterTLSConfig(a.device, data)
}
func (a *app) resolveLocalPrinter(ctx context.Context) error {
	if err := a.selectDevice(ctx, true); err != nil {
		return err
	}
	if a.host == "" {
		a.host = a.cachedPrinterIP(a.device)
		if a.host != "" {
			a.hostFromCache = true
		} else {
			if err := a.refreshPrinterIP(ctx); err != nil {
				return fmt.Errorf("discover printer %s: %w; use --host IP", a.device, err)
			}
		}
	}
	return nil
}
func (a *app) dialPrinter(ctx context.Context) (*bambulab.Printer, error) {
	if err := a.resolveDevice(ctx); err != nil {
		return nil, err
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
	case "ls", "files":
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
	case "snapshot":
		if len(args) > 1 {
			return count(args, 1)
		}
		if len(args) == 0 {
			args = []string{fmt.Sprintf("snapshot-%s.jpg", time.Now().Format("20060102-150405"))}
		}
	default:
		if err := count(args, 1); err != nil {
			return err
		}
	}
	if err := a.resolveLocalPrinter(ctx); err != nil {
		return err
	}
	err := a.runFileCommand(ctx, command, args)
	if err == nil || !a.hostFromCache || ctx.Err() != nil || !staleAddressError(err) {
		return err
	}
	if refreshErr := a.refreshPrinterIP(ctx); refreshErr != nil {
		return errors.Join(err, fmt.Errorf("rediscover printer: %w", refreshErr))
	}
	if command == "upload" || command == "rm" {
		return fmt.Errorf("%w (printer IP refreshed; retry the command)", err)
	}
	return a.runFileCommand(ctx, command, args)
}

func staleAddressError(err error) bool {
	var networkError *net.OpError
	var invalidCertificate *x509.CertificateInvalidError
	var hostnameError *x509.HostnameError
	var unknownAuthority *x509.UnknownAuthorityError
	return errors.As(err, &networkError) || errors.As(err, &invalidCertificate) ||
		errors.As(err, &hostnameError) || errors.As(err, &unknownAuthority)
}

func (a *app) runFileCommand(ctx context.Context, command string, args []string) error {
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
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("camera sent no frame before timeout: %w", err)
			}
			return fmt.Errorf("read camera frame: %w", err)
		}
		if err := writeNewFile(args[0], func(w io.Writer) error { _, err := w.Write(frame); return err }); err != nil {
			return err
		}
		if a.jsonOutput {
			return a.output(map[string]string{"saved": args[0]})
		}
		_, err = fmt.Fprintf(a.out, "Saved snapshot to %s\n", args[0])
		return err
	}
	client, err := bambulab.NewFileClient(bambulab.FileConfig{Address: net.JoinHostPort(a.host, "990"), AccessCode: a.accessCode, TLSConfig: tlsConfig, Timeout: a.timeout})
	if err != nil {
		return err
	}
	switch command {
	case "ls", "files":
		entries, err := client.List(ctx, args[0])
		if err != nil {
			return err
		}
		if a.jsonOutput {
			return a.output(entries)
		}
		w := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
		if _, err := fmt.Fprintln(w, "NAME\tTYPE\tSIZE\tMODIFIED"); err != nil {
			return err
		}
		for _, entry := range entries {
			if _, err := fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", entry.Name, entry.Type, entry.Size, entry.Time.Local().Format("2006-01-02 15:04")); err != nil {
				return err
			}
		}
		return w.Flush()
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
	case "rm":
		if err := client.Delete(ctx, args[0]); err != nil {
			return err
		}
	}
	if a.jsonOutput {
		return a.output(map[string]bool{"success": true})
	}
	if command == "rm" {
		_, err = fmt.Fprintf(a.out, "Removed %s\n", args[0])
		return err
	}
	_, err = fmt.Fprintf(a.out, "%s complete\n", strings.ToUpper(command[:1])+command[1:])
	return err
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
