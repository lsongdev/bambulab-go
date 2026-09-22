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

Global flags go **before** the command. Output is JSON; errors go to stderr and
exit with status 1. `watch` emits one JSON object per line and stops on Ctrl-C.

```sh
# China is the default; set --region global for an international account.
export BAMBU_ACCOUNT='you@example.com'
export BAMBU_PASSWORD='your-password'
bambulab --region global login
unset BAMBU_PASSWORD

# Alternatively, read a password from stdin without putting it in argv:
password-manager-command | bambulab login --account you@example.com --password-stdin

# If login requests verification, submit the received code:
bambulab login --account you@example.com --code 123456

bambulab profile
bambulab devices
bambulab tasks --limit 10
bambulab messages --type 6 --limit 10
bambulab projects
bambulab project PROJECT_ID
bambulab settings
bambulab setting SETTING_ID
bambulab version DEVICE_SERIAL
bambulab status
bambulab logout
```

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
| `BAMBU_CA_FILE` | Trusted printer CA PEM file |
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

To use LAN MQTT, FTPS or the A1/P1 camera, also configure the host, access code and
trusted CA. Obtain the Bambu printer CA from a trusted source (see
[TLS reference](docs/tls.md)); it is not bundled in this repository. Both the
certificate chain and the serial-number identity are checked, including legacy
certificates that identify printers only through their Common Name.

```sh
export BAMBU_HOST='192.168.1.100'
export BAMBU_ACCESS_CODE='LAN_CODE'
export BAMBU_CA_FILE='/path/to/bambu-ca.pem'

bambulab watch
bambulab files /
bambulab --timeout 5m upload model.3mf /model.3mf
bambulab --timeout 5m download /model.3mf downloaded.3mf
bambulab snapshot frame.jpg
bambulab print ftp:///model.3mf --plate 1 --bed-level
bambulab print-gcode /path/on/printer/file.gcode
bambulab gcode 'M105'
bambulab command camera ipcam_timelapse '{"control":"enable"}'
bambulab delete /model.3mf
```

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

Physical printers and live cloud accounts have not been used for validation.
Firmware authorization restrictions can reject commands. Refresh tokens, support
tickets, and some IoT task endpoints have known undocumented/unavailable behavior
in the supplied references. X1 RTSP decoding, proprietary cloud video transport,
and unspecified firmware downgrade procedures are not implemented.
