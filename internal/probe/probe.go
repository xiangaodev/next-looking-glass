// Package probe checks SSH and RDP services with native TCP handshakes.
package probe

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	lgexec "github.com/xiangaodev/next-looking-glass/internal/exec"
)

const (
	overallTimeout   = 10 * time.Second
	handshakeTimeout = 5 * time.Second
	maxSSHBytes      = 8192
	maxRDPPacket     = 1024
)

var rdpNegotiationRequest = []byte{
	0x03, 0x00, 0x00, 0x13,
	0x0e, 0xe0, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x01, 0x00, 0x08, 0x00, 0x03, 0x00, 0x00, 0x00,
}

// Supported reports whether name is a native protocol probe.
func Supported(name string) bool {
	return name == "ssh" || name == "rdp"
}

// Run checks one public SSH or RDP endpoint and returns terminal output lines.
func Run(ctx context.Context, name, rawTarget string) ([]string, error) {
	if !Supported(name) {
		return nil, fmt.Errorf("unknown protocol probe %q", name)
	}
	defaultPort := 22
	if name == "rdp" {
		defaultPort = 3389
	}
	host, port, err := parseTarget(rawTarget, defaultPort)
	if err != nil {
		return nil, err
	}
	cleanHost, err := lgexec.ValidateTarget(host)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, overallTimeout)
	defer cancel()
	ips, err := resolvePublic(ctx, cleanHost)
	if err != nil {
		return nil, err
	}

	protocol := strings.ToUpper(name)
	target := net.JoinHostPort(cleanHost, strconv.Itoa(port))
	lines := []string{"Target: " + target}
	start := time.Now()
	conn, address, err := dialPublic(ctx, ips, port)
	elapsed := time.Since(start)
	if err != nil {
		lines = append(lines,
			fmt.Sprintf("TCP: connection failed after %s", formatDuration(elapsed)),
			"Error: "+err.Error(),
			"Result: "+protocol+" service not confirmed",
		)
		return lines, nil
	}
	defer conn.Close()
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()

	lines = append(lines, fmt.Sprintf("TCP: connected to %s in %s", address, formatDuration(elapsed)))
	if err := setHandshakeDeadline(conn, ctx); err != nil {
		return nil, err
	}

	var details []string
	if name == "ssh" {
		var banner string
		banner, err = readSSHBanner(conn)
		if err == nil {
			details = []string{"SSH: " + banner}
		}
	} else {
		details, err = checkRDP(conn)
	}
	if err != nil {
		lines = append(lines,
			protocol+": handshake failed: "+err.Error(),
			"Result: TCP port is open; "+protocol+" service not confirmed",
		)
		return lines, nil
	}
	lines = append(lines, details...)
	lines = append(lines, "Result: "+protocol+" service confirmed")
	return lines, nil
}

func parseTarget(raw string, defaultPort int) (string, int, error) {
	target := strings.TrimSpace(raw)
	if target == "" {
		return "", 0, errors.New("empty target")
	}
	if len(target) > 270 {
		return "", 0, errors.New("target too long")
	}
	if i := strings.Index(target, "://"); i >= 0 {
		target = target[i+3:]
	}
	if i := strings.IndexAny(target, "/?#"); i >= 0 {
		target = target[:i]
	}

	host := target
	portText := ""
	if strings.HasPrefix(target, "[") {
		end := strings.Index(target, "]")
		if end < 0 {
			return "", 0, errors.New("invalid bracketed target")
		}
		host = target[1:end]
		rest := target[end+1:]
		if rest != "" {
			if !strings.HasPrefix(rest, ":") || len(rest) == 1 {
				return "", 0, errors.New("invalid target or port")
			}
			portText = rest[1:]
		}
	} else if strings.Count(target, ":") == 1 {
		h, p, err := net.SplitHostPort(target)
		if err != nil || p == "" {
			return "", 0, errors.New("invalid target or port")
		}
		host, portText = h, p
	}
	if host == "" {
		return "", 0, errors.New("empty target")
	}
	if portText == "" {
		return host, defaultPort, nil
	}
	for _, r := range portText {
		if r < '0' || r > '9' {
			return "", 0, errors.New("port must be a number between 1 and 65535")
		}
	}
	port64, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port64 == 0 {
		return "", 0, errors.New("port must be between 1 and 65535")
	}
	return host, int(port64), nil
}

func resolvePublic(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if lgexec.IsDisallowedIP(ip) {
			return nil, fmt.Errorf("target %s is not a public address", host)
		}
		return []net.IP{ip}, nil
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve %s: %w", host, err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("%s has no DNS records", host)
	}
	ips := make([]net.IP, 0, len(addresses))
	seen := make(map[string]bool, len(addresses))
	for _, address := range addresses {
		ip := address.IP
		if lgexec.IsDisallowedIP(ip) {
			return nil, fmt.Errorf("%s resolves to non-public address %s", host, ip)
		}
		key := ip.String()
		if !seen[key] {
			seen[key] = true
			ips = append(ips, ip)
		}
	}
	return ips, nil
}

func dialPublic(ctx context.Context, ips []net.IP, port int) (net.Conn, string, error) {
	dialer := &net.Dialer{Timeout: handshakeTimeout}
	var lastErr error
	for _, ip := range ips {
		address := net.JoinHostPort(ip.String(), strconv.Itoa(port))
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err == nil {
			return conn, address, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no usable address")
	}
	return nil, "", lastErr
}

func setHandshakeDeadline(conn net.Conn, ctx context.Context) error {
	deadline := time.Now().Add(handshakeTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	return conn.SetDeadline(deadline)
}

func readSSHBanner(conn net.Conn) (string, error) {
	reader := bufio.NewReader(io.LimitReader(conn, maxSSHBytes+1))
	total := 0
	for lineCount := 0; lineCount < 50; lineCount++ {
		line, err := reader.ReadString('\n')
		total += len(line)
		if total > maxSSHBytes {
			return "", errors.New("identification response too large")
		}
		banner := strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(banner, "SSH-") {
			if len(banner) > 255 || !strings.Contains(banner[4:], "-") {
				return "", errors.New("invalid SSH identification string")
			}
			return banner, nil
		}
		if len(line) > 1024 {
			return "", errors.New("SSH identification line too long")
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", errors.New("connection closed before SSH identification")
			}
			return "", err
		}
	}
	return "", errors.New("too many lines before SSH identification")
}

func checkRDP(conn net.Conn) ([]string, error) {
	for written := 0; written < len(rdpNegotiationRequest); {
		n, err := conn.Write(rdpNegotiationRequest[written:])
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, io.ErrUnexpectedEOF
		}
		written += n
	}

	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	if header[0] != 3 || header[1] != 0 {
		return nil, errors.New("invalid TPKT header")
	}
	length := int(binary.BigEndian.Uint16(header[2:]))
	if length < 11 || length > maxRDPPacket {
		return nil, fmt.Errorf("invalid TPKT length %d", length)
	}
	body := make([]byte, length-4)
	if _, err := io.ReadFull(conn, body); err != nil {
		return nil, err
	}
	if len(body) < 7 || int(body[0]) != len(body)-1 || body[1]&0xf0 != 0xd0 {
		return nil, errors.New("invalid X.224 Connection Confirm")
	}
	if len(body) == 7 {
		return []string{"RDP: legacy Connection Confirm received", "Security: Standard RDP Security"}, nil
	}
	if len(body) != 15 || body[0] != 14 {
		return nil, errors.New("unsupported X.224 Connection Confirm layout")
	}
	if binary.LittleEndian.Uint16(body[9:11]) != 8 {
		return nil, errors.New("invalid RDP negotiation data length")
	}
	value := binary.LittleEndian.Uint32(body[11:15])
	switch body[7] {
	case 2:
		return []string{"RDP: negotiation response received", "Security: " + rdpSecurityName(value)}, nil
	case 3:
		return []string{"RDP: negotiation rejected: " + rdpFailureName(value)}, nil
	default:
		return nil, fmt.Errorf("unexpected RDP negotiation type %d", body[7])
	}
}

func rdpSecurityName(protocol uint32) string {
	switch protocol {
	case 0:
		return "Standard RDP Security"
	case 1:
		return "TLS (Enhanced RDP Security)"
	case 2:
		return "CredSSP / NLA"
	case 4:
		return "RDSTLS"
	case 8:
		return "CredSSP with early user auth"
	case 16:
		return "RDS AAD Auth"
	default:
		return fmt.Sprintf("unknown (0x%x)", protocol)
	}
}

func rdpFailureName(code uint32) string {
	switch code {
	case 1:
		return "SSL required by server"
	case 2:
		return "SSL not allowed by server"
	case 3:
		return "SSL certificate not on server"
	case 4:
		return "inconsistent flags"
	case 5:
		return "CredSSP / NLA required by server"
	case 6:
		return "SSL with user authentication required by server"
	default:
		return fmt.Sprintf("failure code 0x%x", code)
	}
}

func formatDuration(duration time.Duration) string {
	if duration < time.Millisecond {
		return duration.Round(time.Microsecond).String()
	}
	return duration.Round(time.Millisecond).String()
}
