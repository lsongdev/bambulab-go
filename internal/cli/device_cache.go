package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lsongdev/bambulab-go/bambulab"
)

const deviceListTTL = time.Hour
const deviceIPTTL = 15 * time.Minute

type cachedIP struct {
	Address string    `json:"address"`
	SeenAt  time.Time `json:"seen_at"`
}

type deviceCache struct {
	Scope     string              `json:"scope"`
	UpdatedAt time.Time           `json:"updated_at"`
	Devices   []bambulab.Device   `json:"devices"`
	IPs       map[string]cachedIP `json:"ips,omitempty"`
}

func (a *app) deviceCachePath() string {
	base := filepath.Base(a.configPath)
	name := strings.TrimSuffix(base, filepath.Ext(base)) + "-devices.json"
	return filepath.Join(filepath.Dir(a.configPath), name)
}

func (a *app) cacheScope() string {
	sum := sha256.Sum256([]byte(a.region + "\x00" + a.apiURL + "\x00" + a.token))
	return hex.EncodeToString(sum[:])
}

func (a *app) readDeviceCache() deviceCache {
	path := a.deviceCachePath()
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return deviceCache{Scope: a.cacheScope()}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return deviceCache{Scope: a.cacheScope()}
	}
	var cache deviceCache
	if json.Unmarshal(data, &cache) != nil || cache.Scope != a.cacheScope() {
		return deviceCache{Scope: a.cacheScope()}
	}
	return cache
}

func (a *app) saveDeviceCache(cache deviceCache) error {
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	path := a.deviceCachePath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".devices-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (a *app) boundDevices(ctx context.Context) ([]bambulab.Device, deviceCache, error) {
	if err := a.requireToken(); err != nil {
		return nil, deviceCache{}, err
	}
	cache := a.readDeviceCache()
	if cache.Devices != nil && time.Since(cache.UpdatedAt) < deviceListTTL && time.Since(cache.UpdatedAt) >= 0 {
		return cache.Devices, cache, nil
	}
	response, err := a.client.ListDevicesContext(ctx)
	if err != nil {
		return nil, cache, err
	}
	cache.Devices = append([]bambulab.Device{}, response.Devices...)
	cache.UpdatedAt = time.Now()
	_ = a.saveDeviceCache(cache)
	return cache.Devices, cache, nil
}

func (a *app) cachedPrinterIP(serial string) string {
	entry := a.readDeviceCache().IPs[serial]
	if entry.Address == "" || time.Since(entry.SeenAt) < 0 || time.Since(entry.SeenAt) >= deviceIPTTL {
		return ""
	}
	return entry.Address
}

func (a *app) rememberPrinterIP(serial, address string) {
	if serial == "" || address == "" {
		return
	}
	cache := a.readDeviceCache()
	if cache.IPs == nil {
		cache.IPs = make(map[string]cachedIP)
	}
	cache.IPs[serial] = cachedIP{Address: address, SeenAt: time.Now()}
	_ = a.saveDeviceCache(cache)
}

func (a *app) forgetPrinterIP(serial string) {
	cache := a.readDeviceCache()
	delete(cache.IPs, serial)
	_ = a.saveDeviceCache(cache)
}

func (a *app) refreshPrinterIP(ctx context.Context) error {
	a.forgetPrinterIP(a.device)
	printer, err := discoverPrinter(ctx, a.device)
	if err != nil {
		return err
	}
	a.host = printer.IP.String()
	a.hostFromCache = false
	a.rememberPrinterIP(a.device, a.host)
	return nil
}

func (a *app) clearDeviceCache() error {
	err := os.Remove(a.deviceCachePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
