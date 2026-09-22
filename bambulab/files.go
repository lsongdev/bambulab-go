package bambulab

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/jlaffaye/ftp"
)

type FileConfig struct {
	Address    string // host:port; local printers use port 990
	AccessCode string
	TLSConfig  *tls.Config
	Timeout    time.Duration // per operation, defaults to five minutes
}

// FileClient opens an independent implicit-FTPS session per operation. Methods
// may run concurrently. Reader/Writer implementations must not block indefinitely.
type FileClient struct{ config FileConfig }
type FileEntry = ftp.Entry

func NewFileClient(config FileConfig) (*FileClient, error) {
	if _, _, err := net.SplitHostPort(config.Address); err != nil {
		return nil, fmt.Errorf("bambulab: FTPS address must be host:port: %w", err)
	}
	config.AccessCode = strings.TrimSpace(config.AccessCode)
	if config.AccessCode == "" || strings.ContainsAny(config.AccessCode, "\r\n\x00") {
		return nil, fmt.Errorf("bambulab: valid access code is required")
	}
	if config.Timeout == 0 {
		config.Timeout = 5 * time.Minute
	}
	if config.Timeout < 0 {
		return nil, fmt.Errorf("bambulab: timeout must be positive")
	}
	if config.TLSConfig == nil {
		config.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		config.TLSConfig = config.TLSConfig.Clone()
	}
	if config.TLSConfig.ServerName == "" {
		config.TLSConfig.ServerName, _, _ = net.SplitHostPort(config.Address)
	}
	if config.TLSConfig.ClientSessionCache == nil {
		config.TLSConfig.ClientSessionCache = tls.NewLRUClientSessionCache(8)
	}
	return &FileClient{config: config}, nil
}
func validFTPPath(path string) error {
	if path == "" || strings.ContainsAny(path, "\r\n\x00") {
		return fmt.Errorf("bambulab: a valid remote path is required")
	}
	return nil
}
func (f *FileClient) session(ctx context.Context, operation func(*ftp.ServerConn) error) error {
	ctx, cancel := context.WithTimeout(ctx, f.config.Timeout)
	defer cancel()
	var mu sync.Mutex
	var sockets []net.Conn
	cleanup := func() {
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range sockets {
			_ = conn.Close()
		}
	}
	stop := context.AfterFunc(ctx, cleanup)
	defer stop()
	defer cleanup()
	var controlHost string
	dial := func(network, address string) (net.Conn, error) {
		// Some printers advertise a stale PASV IP. Always use the control peer.
		if controlHost != "" {
			_, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			address = net.JoinHostPort(controlHost, port)
		}
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		if err := ctx.Err(); err != nil {
			mu.Unlock()
			conn.Close()
			return nil, err
		}
		sockets = append(sockets, conn)
		mu.Unlock()
		if deadline, ok := ctx.Deadline(); ok {
			_ = conn.SetDeadline(deadline)
		}
		if controlHost == "" {
			controlHost, _, _ = net.SplitHostPort(conn.RemoteAddr().String())
		}
		// Handshake lazily: data-channel handshakes must follow the STOR/RETR command.
		return tls.Client(conn, f.config.TLSConfig), nil
	}
	conn, err := ftp.Dial(f.config.Address, ftp.DialWithTLS(f.config.TLSConfig), ftp.DialWithDialFunc(dial))
	if err == nil {
		if err = conn.Login("bblp", f.config.AccessCode); err == nil {
			err = operation(conn)
		}
		quitErr := conn.Quit()
		if err == nil {
			err = quitErr
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
func (f *FileClient) List(ctx context.Context, path string) (entries []*FileEntry, err error) {
	if err = validFTPPath(path); err != nil {
		return nil, err
	}
	err = f.session(ctx, func(c *ftp.ServerConn) error { var e error; entries, e = c.List(path); return e })
	return
}
func (f *FileClient) Upload(ctx context.Context, path string, source io.Reader) error {
	if err := validFTPPath(path); err != nil {
		return err
	}
	if source == nil {
		return fmt.Errorf("bambulab: upload reader is required")
	}
	return f.session(ctx, func(c *ftp.ServerConn) error { return c.Stor(path, source) })
}
func (f *FileClient) Download(ctx context.Context, path string, target io.Writer) error {
	if err := validFTPPath(path); err != nil {
		return err
	}
	if target == nil {
		return fmt.Errorf("bambulab: download writer is required")
	}
	return f.session(ctx, func(c *ftp.ServerConn) error {
		response, err := c.Retr(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(target, response)
		return errors.Join(copyErr, response.Close())
	})
}
func (f *FileClient) Delete(ctx context.Context, path string) error {
	if err := validFTPPath(path); err != nil {
		return err
	}
	return f.session(ctx, func(c *ftp.ServerConn) error { return c.Delete(path) })
}
func (f *FileClient) Rename(ctx context.Context, from, to string) error {
	if err := validFTPPath(from); err != nil {
		return err
	}
	if err := validFTPPath(to); err != nil {
		return err
	}
	return f.session(ctx, func(c *ftp.ServerConn) error { return c.Rename(from, to) })
}
func (f *FileClient) MakeDir(ctx context.Context, path string) error {
	if err := validFTPPath(path); err != nil {
		return err
	}
	return f.session(ctx, func(c *ftp.ServerConn) error { return c.MakeDir(path) })
}
func (f *FileClient) RemoveDir(ctx context.Context, path string) error {
	if err := validFTPPath(path); err != nil {
		return err
	}
	return f.session(ctx, func(c *ftp.ServerConn) error { return c.RemoveDir(path) })
}
