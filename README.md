# bambulab-go

Go SDK and CLI for Bambu Lab cloud APIs and LAN printers. Requires Go 1.24+.
The implementation follows the protocol references in [docs](docs/). These are
unofficial APIs: availability depends on region, printer model, firmware and
account permissions. No physical printer is needed to run the tests.

## Install

```sh
go get github.com/lsongdev/bambulab-go/bambulab
go install github.com/lsongdev/bambulab-go/cmd/bambulab@latest

# Build this checkout
go build -o /tmp/bambulab ./cmd/bambulab
/tmp/bambulab help
```

## CLI

Global flags go **before** the command, except `--device` and `--json`, which
may also appear after it. `devices` prints a table; `devices --json` prints the
devices array with discovered LAN IPs. Errors go to stderr and exit with status 1. `watch` emits
one JSON object per line and stops on Ctrl-C.

```sh
# China is the default; set --region global for an international account.
bambulab --region global login
# Enter your account/email and password when prompted. If verification is
# required, enter the code; the command keeps prompting until login succeeds.

# For noninteractive use, supply credentials through environment variables:
export BAMBU_ACCOUNT='you@example.com'
export BAMBU_PASSWORD='your-password'
bambulab --region global login
unset BAMBU_PASSWORD

# Alternatively, read a password from stdin without putting it in argv:
password-manager-command | bambulab login --account you@example.com --password-stdin

# If noninteractive login requests verification, submit the received code:
bambulab login --account you@example.com --code 123456

bambulab profile
bambulab devices
bambulab devices --json
bambulab status
bambulab version
bambulab state
bambulab state --json
bambulab --device PRINTER_SERIAL status
bambulab state --device PRINTER_SERIAL
bambulab tasks --limit 10
bambulab messages --type 6 --limit 10
bambulab projects
bambulab project PROJECT_ID
bambulab settings
bambulab setting SETTING_ID
bambulab logout
```

`devices` shows bound printers, their discovered LAN IPs, and access codes. `status` shows the selected
printer's cloud print-job summary. `state` connects over MQTT to report live
run state, progress, temperatures, lights and camera availability. The current
status report does not provide head X/Y/Z coordinates, so `state` marks them
unavailable. Controls use the same printer selection rule:

```sh
bambulab pause
bambulab resume --device PRINTER_SERIAL
bambulab stop
bambulab speed 2
bambulab light on
```

The CLI caches the bound-device list for one hour and discovered IPs for 15
minutes in `credentials-devices.json` beside the credentials file. The cache is
scoped to the account token, region, and API server, stored with mode `0600`,
and removed by `logout`. An expired IP is discovered again; a cached IP that
fails to connect is rediscovered. Explicit `--device`, `--host`, and
`--access-code` values still take precedence.

If `--device SERIAL` (or `BAMBU_DEVICE_ID`) is omitted, commands that operate
on one printer use the first printer in the bound list. Use `devices` to see the
order. For LAN file operations and the A1/P1 camera, the CLI discovers the
printer's IP from its UDP announcements and gets the access code from the
account's bound-device list:

```sh
bambulab snapshot
bambulab snapshot frame.jpg
bambulab snapshot frame.jpg --device PRINTER_SERIAL
bambulab snapshot --json
bambulab ls /
bambulab ls / --json
```

Without a filename, the image is saved as `snapshot-YYYYMMDD-HHMMSS.jpg`.
The default output is a short confirmation; `--json` outputs `{"saved":"..."}`.
If local discovery is unavailable, specify `--host IP`; `--access-code` remains
available when the account cannot supply it. See the [discovery flow](docs/discovery.md)
for the UDP packet format, matching rules, SDK APIs, and fallback behavior.

The Bambu printer CA bundle is built into the SDK and used to verify the
printer's certificate and serial number. `--ca FILE` overrides the bundle if
Bambu changes its certificate chain. This snapshot command uses the local LAN
camera stream. Cloud video uses a separate relay protocol, so it has no `cloud
snapshot` command here.

Login saves tokens and region to the user configuration directory:
`bambulab/credentials.json` (`$XDG_CONFIG_HOME/bambulab/credentials.json` or
`~/.config/bambulab/credentials.json` on Linux). Files are written atomically with
mode `0600`; passwords are not saved and tokens are not printed by login.
`--config PATH` chooses another file. `logout` removes local credentials; it does
not revoke tokens on the server or clear environment variables.

Flags override environment variables. Explicit/environment tokens take precedence
over saved credentials. Changing region does not reuse the saved region's token.
Environment variables:

| Variable | Purpose |
| --- | --- |
| `BAMBU_ACCOUNT`, `BAMBU_PASSWORD`, `BAMBU_CODE` | Login credentials; a code takes precedence over the password environment variable |
| `BAMBU_REGION` | `china` or `global` |
| `BAMBU_ACCESS_TOKEN` | Cloud access token |
| `BAMBU_DEVICE_ID` | Printer serial number |
| `BAMBU_HOST` | LAN IP/hostname, without a port |
| `BAMBU_ACCESS_CODE` | LAN access code |
| `BAMBU_CA_FILE` | Override bundled printer CA PEM file |
| `BAMBU_API_URL` | HTTP API override for testing or a trusted proxy |

Cloud MQTT obtains the numeric user ID from the preferences endpoint:

```sh
export BAMBU_DEVICE_ID='PRINTER_SERIAL'
bambulab watch
bambulab pause
bambulab resume
bambulab speed 2
bambulab light on
bambulab stop
```

LAN MQTT uses the host and access code when explicitly configured. FTPS and the
A1/P1 camera can discover the host and access code after login. The Bambu CA is
built in. Both the certificate chain and the serial-number identity are checked,
including legacy certificates that identify printers only through their Common
Name. Use `BAMBU_CA_FILE` to override the bundled trust roots.

```sh
bambulab watch
bambulab ls /
bambulab --timeout 5m upload model.3mf /model.3mf
bambulab --timeout 5m download /model.3mf downloaded.3mf
bambulab snapshot frame.jpg
bambulab print ftp:///model.3mf --plate 1 --bed-level
bambulab print-gcode /path/on/printer/file.gcode
bambulab gcode 'M105'
bambulab command camera ipcam_timelapse '{"control":"enable"}'
bambulab rm /model.3mf
```

`ls` and `files` print a file table by default and an entries array with `--json`. Upload,
download and `rm` print a short confirmation by default; `--json` returns a
success object. Set `BAMBU_HOST` and `BAMBU_ACCESS_CODE` when discovery or the
account's bound-device access code is unavailable.

Uploading replaces the named remote file. Downloads and snapshots refuse to
overwrite existing local files and remove incomplete output on failure. Files
use implicit FTPS on port 990; camera snapshots use the A1/P1 JPEG stream on port
6000. X1 uses RTSPS instead; use `RTSPURL` in the SDK with a compatible player.
Uploads do not start prints automatically.

`bambulab api METHOD /path [JSON]` exposes additional cloud endpoints, and
`bambulab command SECTION COMMAND [JSON]` exposes additional MQTT commands. Pass
`-` as JSON to read stdin. `--timeout` defaults to 30 seconds; for `watch` it limits
connection/command operations, not the lifetime of the subscription.

## Cloud SDK

```go
package main

import (
    "context"
    "fmt"
    "os"
    "time"

    "github.com/lsongdev/bambulab-go/bambulab"
)

func main() {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    client := bambulab.NewClient(
        bambulab.WithRegion(bambulab.RegionGlobal),
        bambulab.WithAccessToken(os.Getenv("BAMBU_ACCESS_TOKEN")),
    )
    devices, err := client.ListDevicesContext(ctx)
    if err != nil { panic(err) }
    for _, device := range devices.Devices {
        fmt.Println(device.ID, device.Name, device.Online)
    }
}
```

`LoginContext` accepts an account and exactly one of `Password` or `Code`, and
stores a returned access token on the client. A `LoginType == "verifyCode"`
response without a token requires another login with the code. There is no
automatic refresh/retry: the documented refresh endpoint may return 401 even for
valid tokens, and retrying printer operations can repeat physical actions.

The original `NewClient()`, `Login`, `GetProfile`, `ListDevices` and public
`AccessToken` field remain available. Use `SetAccessToken`/`Token` for concurrent
access; do not assign the public field concurrently with requests.
`WithHTTPClient` and `WithBaseURL` support custom transports and testing.

| Area | Methods |
| --- | --- |
| Account | `LoginContext`, `RefreshToken`, `GetProfileContext`, `GetPreference` |
| Devices | `ListDevicesContext`, `UpdateDeviceInfo`, `GetDeviceVersion`, `GetPrintStatus`, `GetTTCode` |
| History | `ListMessages`, `ListTasks`, `CreateTask`, `GetTask`, `GetTicket`, `GetNotification` |
| Slicer | `GetResources`, `ListSettings`, `GetSetting` |
| Projects | `ListProjects`, `GetProject`, `GetProjectProfile` |
| Extension | `Do(ctx, method, path, query, body, result)` |

Use `errors.As(err, &apiError)` with `var apiError *bambulab.APIError` to inspect
HTTP status, service code and message. Response bodies are bounded to 16 MiB.
Undocumented schemas use `Object`/`json.RawMessage` instead of discarding fields.
Pagination methods fetch one page; supply `After` to continue.

## Printer SDK

```go
caPEM, err := os.ReadFile("/path/to/bambu-ca.pem")
if err != nil { return err }
tlsConfig, err := bambulab.PrinterTLSConfig(serial, caPEM)
if err != nil { return err }
printer, err := bambulab.DialMQTT(ctx,
    bambulab.LocalMQTTConfig(host, serial, accessCode, tlsConfig))
if err != nil { return err }
defer printer.Close()

if err := printer.PushAll(ctx); err != nil { return err }
for {
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-printer.Done():
        return bambulab.ErrClosed
    case err := <-printer.Errors():
        return err
    case <-printer.Reports():
        status, err := printer.Status()
        if err != nil { return err }
        fmt.Println(status.GCodeState, status.Percent)
    }
}
```

For cloud connections, pass `CloudMQTTConfig(region, preference.ID, serial, token)`
to `DialMQTT`. TLS verification is enabled by default.

Printer methods cover all specified request commands in [MQTT reference](docs/mqtt.md):
pause/resume/stop, speed, G-code, project printing, object skipping, calibration,
AMS control/settings, lighting, camera recording/timelapse, XCam features,
versions, access code and firmware upgrade commands. G-code extensions/macros in
[G-code reference](docs/gcode.md) can be sent through `SendGCode`.

`Send` and typed command methods wait for MQTT delivery; this does **not** prove
that the printer accepted or executed a command. `Request` waits for a matching
section/command/sequence report and returns `CommandError` for rejection. Some
commands, such as `pushing.pushall`, respond with another section: use `Send` or
`PushAll` and consume `Reports` for those.

`Status` decodes common fields; `Snapshot` returns all merged JSON fields. P1
incremental updates merge recursively; arrays replace older arrays. Missing
fields remain unknown until reported, so a typed zero value is not proof of a
physical state. Request an initial full status; avoid repeated `PushAll` calls
more often than every five minutes on P1P.

`Reports` and `Errors` are bounded channels. Slow consumers may lose individual
reports (`DroppedReports` counts them), but state is still merged and pending
requests still receive replies. Select on `Done` for disconnection; channels are
not closed. Automatic reconnection/replay is disabled: dial again after loss.
Context cancellation cannot undo a command already sent to a printer.

`NewFileClient(FileConfig{...})` offers `List`, `Upload`, `Download`, `Delete`,
`Rename`, `MakeDir`, and `RemoveDir`, each using its own FTPS connection with
context cancellation. `DialCamera`/`ReadFrame` provide bounded JPEG frames over
TLS. A camera read error closes the stream; reconnect before reading again.
User-supplied file readers/writers must not block indefinitely.

## Verification and limits

```sh
go test -race -cover ./...
go vet ./...
```

Tests cover HTTP routing/authentication/errors, CLI credentials and validation,
MQTT command correlation/concurrent sequences/status merging, and local TLS
servers for MQTT, FTPS and camera transfers. They require permission to listen on
loopback; they never contact real printers or cloud accounts.

Automated tests do not use physical printers or live cloud accounts. Read-only
`devices`, `ls`, and `state` commands have also been checked against an A1 mini.
Firmware authorization restrictions can reject commands. Refresh tokens, support
tickets, and some IoT task endpoints have known undocumented/unavailable behavior
in the supplied references. X1 RTSP decoding, proprietary cloud video transport,
and unspecified firmware downgrade procedures are not implemented.
