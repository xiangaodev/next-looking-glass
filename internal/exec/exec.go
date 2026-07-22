// Package exec runs network diagnostic commands safely and streams output.
package exec

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Command is a whitelisted diagnostic command definition.
type Command struct {
	Name    string
	Bin     string
	Args    func(target string) []string
	Timeout time.Duration
	// StallTimeout kills the process if no output is produced within this
	// window (used for traceroute which can hang on silent hops). Zero
	// disables stall detection.
	StallTimeout time.Duration
	// NeedsIPv6 marks commands that require an IPv6-capable target.
	NeedsIPv6 bool
}

// registry maps public command names to their definitions.
// On macOS/BSD, ping is a single binary without -4/-6 (use ping/ping6);
// on Linux iputils, ping supports -4/-6 directly. Build args accordingly.
var registry = map[string]Command{
	"ping": {
		Name:    "ping",
		Bin:     "ping",
		Args:    func(t string) []string { return pingArgs(false, t) },
		Timeout: 30 * time.Second,
	},
	"ping6": {
		Name:      "ping6",
		Bin:       ping6Bin(),
		Args:      func(t string) []string { return pingArgs(true, t) },
		Timeout:   30 * time.Second,
		NeedsIPv6: true,
	},
	"traceroute": {
		Name:    "traceroute",
		Bin:     "nexttrace",
		Args:    func(t string) []string { return []string{"--json", "--queries", "3", "--max-hops", "30", t} },
		Timeout: 90 * time.Second,
		// nexttrace is fast and self-terminating; no stall watchdog needed.
	},
	"traceroute6": {
		Name:      "traceroute6",
		Bin:       "nexttrace",
		Args:      func(t string) []string { return []string{"--json", "--queries", "3", "--max-hops", "30", t} },
		Timeout:   90 * time.Second,
		NeedsIPv6: true,
	},
	"mtr": {
		Name:    "mtr",
		Bin:     "nexttrace",
		Args:    func(t string) []string { return []string{"--mtr", "--raw", "--no-rdns", t} },
		Timeout: 120 * time.Second,
	},
	"mtr6": {
		Name:      "mtr6",
		Bin:       "nexttrace",
		Args:      func(t string) []string { return []string{"--mtr", "--raw", "--no-rdns", t} },
		Timeout:   120 * time.Second,
		NeedsIPv6: true,
	},
	"host": {
		Name:    "host",
		Bin:     "host",
		Args:    func(t string) []string { return []string{t} },
		Timeout: 15 * time.Second,
	},
}

// isLinux reports whether we target GNU/Linux (iputils / GNU traceroute).
const isLinux = runtime.GOOS == "linux"

func pingArgs(v6 bool, t string) []string {
	if isLinux {
		if v6 {
			return []string{"-6", "-c", "4", "-W", "2", t}
		}
		return []string{"-4", "-c", "4", "-W", "2", t}
	}
	// macOS / BSD: -c count, -t timeout(seconds as TTL flag differs); use -c only.
	return []string{"-c", "4", t}
}

func ping6Bin() string {
	if isLinux {
		return "ping"
	}
	return "ping6"
}

// OutputFormat describes how the frontend should render a command's output.
type OutputFormat string

const (
	FormatText      OutputFormat = "text"          // plain text (ping/host)
	FormatTraceJSON OutputFormat = "trace-json"    // nexttrace --json document
	FormatTraceRaw  OutputFormat = "trace-raw-mtr" // nexttrace --mtr --raw stream
)

// FormatOf returns the rendering format for a command.
func FormatOf(name string) OutputFormat {
	switch name {
	case "traceroute", "traceroute6":
		return FormatTraceJSON
	case "mtr", "mtr6":
		return FormatTraceRaw
	default:
		return FormatText
	}
}

// Lookup returns the command definition for name.
func Lookup(name string) (Command, bool) {
	c, ok := registry[name]
	return c, ok
}

// Names returns the list of supported command names.
func Names() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}

var (
	domainRe = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,63}$`)
	// strip a leading scheme if the user pasted a URL
	urlSchemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`)
)

// ValidateTarget normalises and validates a user supplied host. It returns
// the cleaned host or an error. Private, loopback, link-local, multicast and
// unspecified addresses are rejected for both IPv4 and IPv6.
func ValidateTarget(raw string) (string, error) {
	host := strings.TrimSpace(raw)
	if host == "" {
		return "", errors.New("empty target")
	}
	if len(host) > 253 {
		return "", errors.New("target too long")
	}

	// If the user pasted a URL, keep only the host part.
	if urlSchemeRe.MatchString(host) {
		host = urlSchemeRe.ReplaceAllString(host, "")
	}
	// Strip path/query noise.
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	// Handle IPv6 literal with brackets, and host:port.
	if strings.HasPrefix(host, "[") {
		if end := strings.Index(host, "]"); end >= 0 {
			host = host[1:end]
		}
	} else if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, ".")
	if host == "" {
		return "", errors.New("invalid target")
	}

	// IP literal path.
	if ip := net.ParseIP(host); ip != nil {
		if IsDisallowedIP(ip) {
			return "", fmt.Errorf("target %s is not a public address", host)
		}
		return host, nil
	}

	// Domain path: validate syntax, resolve and verify all IPs are public.
	// Pre-resolution prevents DNS rebinding between validation and execution.
	if !domainRe.MatchString(host) {
		return "", errors.New("invalid hostname or IP address")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return "", fmt.Errorf("cannot resolve %s: %w", host, err)
	}
	if len(ips) == 0 {
		return "", fmt.Errorf("%s has no DNS records", host)
	}
	for _, ip := range ips {
		if IsDisallowedIP(ip) {
			return "", fmt.Errorf("%s resolves to non-public address %s", host, ip)
		}
	}
	return host, nil
}

// IsDisallowedIP reports whether ip is private, loopback, link-local,
// multicast, unspecified, or otherwise not a public unicast address.
func IsDisallowedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsMulticast() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() {
		return true
	}
	// IPv4 special ranges.
	if ip4 := ip.To4(); ip4 != nil {
		switch {
		case ip4[0] == 0: // 0.0.0.0/8
			return true
		case ip4[0] == 100 && ip4[1]&0xC0 == 64: // CGNAT 100.64.0.0/10
			return true
		case ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 0: // IETF protocol assignments
			return true
		case ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 2: // TEST-NET-1
			return true
		case ip4[0] == 198 && ip4[1] == 51 && ip4[2] == 100: // TEST-NET-2
			return true
		case ip4[0] == 203 && ip4[1] == 0 && ip4[2] == 113: // TEST-NET-3
			return true
		case ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19): // benchmarking
			return true
		case ip4[0] >= 240: // reserved
			return true
		}
		return false
	}
	// IPv6 documentation 2001:db8::/32
	if p := ip.To16(); p != nil {
		if p[0] == 0x20 && p[1] == 0x01 && p[2] == 0x0d && p[3] == 0xb8 {
			return true
		}
	}
	return false
}

// Runner streams lines from a running command.
type Runner struct {
	Lines <-chan string
	done  chan struct{}
	cmd   *exec.Cmd
}

// Kill terminates the process group.
func (r *Runner) Kill() {
	if r.cmd != nil && r.cmd.Process != nil {
		// Negative PID kills the whole group.
		_ = syscall.Kill(-r.cmd.Process.Pid, syscall.SIGKILL)
	}
}

// Run starts the command and streams stdout+stderr lines.
// The Lines channel is closed when the command exits or ctx is cancelled.
func Run(ctx context.Context, name string, target string) (*Runner, error) {
	cmd, ok := Lookup(name)
	if !ok {
		return nil, fmt.Errorf("unknown command %q", name)
	}
	clean, err := ValidateTarget(target)
	if err != nil {
		return nil, err
	}

	bin, err := exec.LookPath(cmd.Bin)
	if err != nil {
		return nil, fmt.Errorf("%s is not installed on this server", cmd.Bin)
	}

	ctx, cancel := context.WithTimeout(ctx, cmd.Timeout)

	c := exec.CommandContext(ctx, bin, cmd.Args(clean)...)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := c.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderr, err := c.StderrPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := c.Start(); err != nil {
		cancel()
		return nil, err
	}

	raw := make(chan string, 32)
	done := make(chan struct{})
	r := &Runner{done: done, cmd: c}

	// Merge stdout and stderr into one stream.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); streamLines(stdout, raw, done) }()
	go func() { defer wg.Done(); streamLines(stderr, raw, done) }()

	go func() {
		wg.Wait()
		_ = c.Wait()
		close(done)
		close(raw)
		cancel()
	}()

	// Wrap with stall detection for traceroute-like commands.
	if cmd.StallTimeout > 0 {
		wrapped := make(chan string, 32)
		r.Lines = wrapped
		go func() {
			defer close(wrapped)
			timer := time.NewTimer(cmd.StallTimeout)
			defer timer.Stop()
			for {
				select {
				case l, ok := <-raw:
					if !ok {
						return
					}
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(cmd.StallTimeout)
					select {
					case wrapped <- l:
					case <-done:
						return
					}
				case <-timer.C:
					select {
					case wrapped <- fmt.Sprintf("*** no response for %s, aborting (silent hops) ***", cmd.StallTimeout):
					case <-done:
					}
					r.Kill()
					return
				case <-done:
					return
				}
			}
		}()
	} else {
		r.Lines = raw
	}

	return r, nil
}

func streamLines(rc io.ReadCloser, out chan<- string, done <-chan struct{}) {
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 0, 64*1024), 256*1024)
	for sc.Scan() {
		select {
		case out <- sc.Text():
		case <-done:
			_ = rc.Close()
			return
		}
	}
}
