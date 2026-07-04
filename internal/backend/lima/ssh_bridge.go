package lima

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/portbridge"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const limaSSHConnectTimeout = 15 * time.Second

type limaSSHConfig struct {
	HostName                         string
	Port                             string
	User                             string
	IdentityFile                     string
	UserKnownHostsFile               string
	StrictHostKeyChecking            string
	NoHostAuthenticationForLocalhost string
}

func (b Backend) startSSHHostToGuestBridge(ctx context.Context, instanceName, guestWork string, env []string, spec portbridge.Spec) (*portbridge.Bridge, error) {
	if strings.TrimSpace(instanceName) == "" {
		return nil, errors.New("lima host-to-guest bridge requires instance name")
	}
	if strings.TrimSpace(guestWork) == "" {
		return nil, errors.New("lima host-to-guest bridge requires guest workdir")
	}
	targetHost, targetPort, err := normalizeGuestLoopbackTarget(spec.TargetAddress)
	if err != nil {
		return nil, err
	}
	client, err := b.newSSHClient(ctx, instanceName)
	if err != nil {
		return nil, err
	}
	targetAddress := net.JoinHostPort(targetHost, targetPort)
	connector := func(_ context.Context, inbound net.Conn) error {
		outbound, err := client.Dial("tcp", targetAddress)
		if err != nil {
			return err
		}
		defer outbound.Close()
		copyBidirectional(inbound, outbound)
		return nil
	}
	bridge, err := portbridge.StartWithConnector(ctx, spec, connector, client)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	return bridge, nil
}

func normalizeGuestLoopbackTarget(address string) (string, string, error) {
	targetHost, targetPort, err := net.SplitHostPort(address)
	if err != nil {
		return "", "", fmt.Errorf("lima host-to-guest bridge target is invalid: %w", err)
	}
	targetHost = strings.Trim(targetHost, "[]")
	if targetHost == "" || targetHost == "localhost" {
		targetHost = "127.0.0.1"
	}
	if parsed := net.ParseIP(targetHost); parsed == nil || !parsed.IsLoopback() {
		return "", "", errors.New("lima host-to-guest bridge target must be guest loopback")
	}
	if _, err := net.LookupPort("tcp", targetPort); err != nil {
		return "", "", fmt.Errorf("lima host-to-guest bridge target port is invalid: %w", err)
	}
	return targetHost, targetPort, nil
}

func (b Backend) newSSHClient(ctx context.Context, instanceName string) (*ssh.Client, error) {
	cfg, err := readLimaSSHConfig(instanceName)
	if err != nil {
		return nil, err
	}
	clientConfig, err := cfg.clientConfig()
	if err != nil {
		return nil, err
	}
	address := net.JoinHostPort(cfg.HostName, cfg.Port)
	raw, err := (&net.Dialer{Timeout: limaSSHConnectTimeout}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("dial lima ssh: %w", err)
	}
	_ = raw.SetDeadline(sshConnectDeadline(ctx))
	conn, chans, reqs, err := ssh.NewClientConn(raw, address, clientConfig)
	if err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("start lima ssh client: %w", err)
	}
	_ = raw.SetDeadline(time.Time{})
	return ssh.NewClient(conn, chans, reqs), nil
}

func sshConnectDeadline(ctx context.Context) time.Time {
	deadline := time.Now().Add(limaSSHConnectTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		return ctxDeadline
	}
	return deadline
}

func readLimaSSHConfig(instanceName string) (limaSSHConfig, error) {
	path, err := limaSSHConfigPath(instanceName)
	if err != nil {
		return limaSSHConfig{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return limaSSHConfig{}, fmt.Errorf("read lima ssh config: %w", err)
	}
	cfg, err := parseLimaSSHConfig(string(data))
	if err != nil {
		return limaSSHConfig{}, err
	}
	if cfg.HostName == "" {
		return limaSSHConfig{}, errors.New("lima ssh config missing HostName")
	}
	if cfg.Port == "" {
		return limaSSHConfig{}, errors.New("lima ssh config missing Port")
	}
	if cfg.User == "" {
		return limaSSHConfig{}, errors.New("lima ssh config missing User")
	}
	if cfg.IdentityFile == "" {
		return limaSSHConfig{}, errors.New("lima ssh config missing IdentityFile")
	}
	cfg.IdentityFile = expandSSHConfigPath(cfg.IdentityFile)
	cfg.UserKnownHostsFile = expandSSHConfigPath(cfg.UserKnownHostsFile)
	return cfg, nil
}

func limaSSHConfigPath(instanceName string) (string, error) {
	instanceName = strings.TrimSpace(instanceName)
	if instanceName == "" {
		return "", errors.New("lima instance name is required")
	}
	limaHome := strings.TrimSpace(os.Getenv("LIMA_HOME"))
	if limaHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		limaHome = filepath.Join(home, ".lima")
	}
	return filepath.Join(limaHome, instanceName, "ssh.config"), nil
}

func parseLimaSSHConfig(data string) (limaSSHConfig, error) {
	var cfg limaSSHConfig
	for _, raw := range strings.Split(data, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, " ")
		if !ok {
			key, value, ok = strings.Cut(line, "\t")
			if !ok {
				continue
			}
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(stripSSHConfigComment(value))
		if value == "" {
			continue
		}
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}
		switch key {
		case "hostname":
			cfg.HostName = value
		case "port":
			cfg.Port = value
		case "user":
			cfg.User = value
		case "identityfile":
			if cfg.IdentityFile == "" {
				cfg.IdentityFile = value
			}
		case "userknownhostsfile":
			cfg.UserKnownHostsFile = value
		case "stricthostkeychecking":
			cfg.StrictHostKeyChecking = strings.ToLower(value)
		case "nohostauthenticationforlocalhost":
			cfg.NoHostAuthenticationForLocalhost = strings.ToLower(value)
		}
	}
	return cfg, nil
}

func stripSSHConfigComment(value string) string {
	inQuote := false
	for i, r := range value {
		switch r {
		case '"':
			inQuote = !inQuote
		case '#':
			if !inQuote {
				return strings.TrimSpace(value[:i])
			}
		}
	}
	return value
}

func expandSSHConfigPath(value string) string {
	if value == "" {
		return ""
	}
	if value == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
		return value
	}
	if strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(value, "~/"))
		}
	}
	return value
}

func (cfg limaSSHConfig) clientConfig() (*ssh.ClientConfig, error) {
	key, err := os.ReadFile(cfg.IdentityFile)
	if err != nil {
		return nil, fmt.Errorf("read lima ssh identity: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("parse lima ssh identity: %w", err)
	}
	hostKeyCallback, err := cfg.hostKeyCallback()
	if err != nil {
		return nil, err
	}
	return &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeyCallback,
		Timeout:         limaSSHConnectTimeout,
	}, nil
}

func (cfg limaSSHConfig) hostKeyCallback() (ssh.HostKeyCallback, error) {
	knownHostsFile := strings.TrimSpace(cfg.UserKnownHostsFile)
	if knownHostsFile != "" && !isDevNullPath(knownHostsFile) && cfg.StrictHostKeyChecking != "no" {
		callback, err := knownhosts.New(knownHostsFile)
		if err != nil {
			return nil, fmt.Errorf("load lima ssh known hosts: %w", err)
		}
		return callback, nil
	}
	if isDevNullPath(knownHostsFile) || cfg.NoHostAuthenticationForLocalhost == "yes" || cfg.StrictHostKeyChecking == "no" {
		return loopbackUnpinnedHostKeyCallback(), nil
	}
	return nil, errors.New("lima ssh config does not define a usable host key policy")
}

func isDevNullPath(path string) bool {
	return filepath.Clean(strings.TrimSpace(path)) == filepath.Clean(os.DevNull)
}

func loopbackUnpinnedHostKeyCallback() ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, _ ssh.PublicKey) error {
		host := hostname
		if splitHost, _, err := net.SplitHostPort(hostname); err == nil {
			host = splitHost
		}
		host = strings.Trim(host, "[]")
		hostLoopback := false
		if host == "" || host == "localhost" {
			hostLoopback = true
		} else if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			hostLoopback = true
		}
		if !hostLoopback {
			return fmt.Errorf("lima unpinned ssh host key policy only permits loopback endpoints: %s", hostname)
		}
		if tcp, ok := remote.(*net.TCPAddr); ok && tcp.IP != nil && !tcp.IP.IsLoopback() {
			return fmt.Errorf("lima unpinned ssh host key policy only permits loopback remotes: %s", remote.String())
		}
		return nil
	}
}

type closeWriter interface {
	CloseWrite() error
}

func copyBidirectional(left, right net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go copyOneWay(&wg, left, right)
	go copyOneWay(&wg, right, left)
	wg.Wait()
}

func copyOneWay(wg *sync.WaitGroup, dst, src net.Conn) {
	defer wg.Done()
	_, _ = io.Copy(dst, src)
	if cw, ok := dst.(closeWriter); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = dst.Close()
}
