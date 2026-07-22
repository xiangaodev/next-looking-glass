// Package server wires up HTTP handlers, streaming responses and security
// middleware for the looking glass.
package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/xiangaodev/next-looking-glass/internal/config"
	lgexec "github.com/xiangaodev/next-looking-glass/internal/exec"
	"github.com/xiangaodev/next-looking-glass/internal/i18n"
	"github.com/xiangaodev/next-looking-glass/internal/ratelimit"
)

// Server is the HTTP front end.
type Server struct {
	cfg     *config.Config
	lim     *ratelimit.Limiter
	tpl     *template.Template
	mux     *http.ServeMux
	httpSrv *http.Server
}

// New builds a Server from cfg. tpl is the parsed index template; staticFS
// serves CSS/JS under /static/ (may be nil to skip).
func New(cfg *config.Config, tpl *template.Template, staticFS fs.FS) *Server {
	s := &Server{
		cfg: cfg,
		lim: ratelimit.New(cfg.RateLimit.LightPerHour, cfg.RateLimit.HeavyPerHour),
		tpl: tpl,
		mux: http.NewServeMux(),
	}
	s.routes(staticFS)
	return s
}

func (s *Server) routes(staticFS fs.FS) {
	s.mux.HandleFunc("/", s.withSecurity(s.handleIndex))
	s.mux.HandleFunc("/api/info", s.withSecurity(s.handleInfo))
	s.mux.HandleFunc("/api/diag", s.withSecurity(s.handleDiag))
	s.mux.HandleFunc("/download/", s.withSecurity(s.handleDownload))
	s.mux.HandleFunc("/api/upload", s.withSecurity(s.handleUpload))
	s.mux.HandleFunc("/api/ping", s.withSecurity(s.handlePing))
	s.mux.HandleFunc("/api/speedtest/begin", s.withSecurity(s.handleSpeedtestBegin))
	s.mux.HandleFunc("/api/fasttrace", s.withSecurity(s.handleFastTrace))
	s.mux.HandleFunc("/api/unlock", s.withSecurity(s.handleUnlock))
	if staticFS != nil {
		s.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))
	}
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	s.httpSrv = &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: streaming endpoints are long-lived.
		IdleTimeout: 60 * time.Second,
	}
	log.Printf("listening on %s", s.cfg.Listen)
	return s.httpSrv.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Shutdown(ctx)
}

// ---- middleware --------------------------------------------------------

func (s *Server) withSecurity(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		next(w, r)
	}
}

// clientIP extracts the client IP, honouring X-Forwarded-For when trusted.
func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if ip := strings.TrimSpace(strings.Split(xff, ",")[0]); ip != "" {
				return ip
			}
		}
		if xr := r.Header.Get("X-Real-IP"); xr != "" {
			return strings.TrimSpace(xr)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---- handlers ----------------------------------------------------------

type pageData struct {
	SiteName       string
	SiteURL        string
	ServerLocation string
	IPv4           string
	IPv6           string
	Nodes          []config.Node
	CurrentURL     string
	ClientIP       string
	HasIPv6        bool
	Year           int
	Lang           string
	LogoChar       string
	LogoSrc        template.URL
	FaviconSrc     template.URL
	I18N           map[string]string
}

// T looks up a translation key in the page's language map.
func (d pageData) T(key string) string {
	if v, ok := d.I18N[key]; ok {
		return v
	}
	return key
}

// I18NJSON marshals the translation map as a raw JSON object for the front end.
func (d pageData) I18NJS() template.JS {
	b, _ := json.Marshal(d.I18N)
	return template.JS(b)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
		lang := i18n.DetectLang(r, i18n.Lang(s.cfg.DefaultLang))
		d := pageData{
			SiteName:       s.cfg.SiteName,
			SiteURL:        s.cfg.SiteURL,
			ServerLocation: s.cfg.ServerLocation,
			IPv4:           s.cfg.IPv4,
			IPv6:           s.cfg.IPv6,
			Nodes:          s.cfg.Nodes,
			CurrentURL:     "https://" + r.Host,
			ClientIP:       s.clientIP(r),
			HasIPv6:        s.cfg.IPv6 != "",
			Year:           time.Now().Year(),
			Lang:           string(lang),
			LogoChar:       s.cfg.LogoChar(),
			LogoSrc:        s.cfg.LogoSrc(),
			FaviconSrc:     s.cfg.FaviconSrc(),
			I18N:           i18n.Map(lang),
		}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tpl.Execute(w, d); err != nil {
		log.Printf("template error: %v", err)
	}
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"site_name":       s.cfg.SiteName,
		"server_location": s.cfg.ServerLocation,
		"ipv4":            s.cfg.IPv4,
		"ipv6":            s.cfg.IPv6,
		"your_ip":         s.clientIP(r),
		"nodes":           s.cfg.Nodes,
	})
}

// handleDiag streams diagnostic command output line by line.
func (s *Server) handleDiag(w http.ResponseWriter, r *http.Request) {
	cmd := r.URL.Query().Get("cmd")
	target := r.URL.Query().Get("target")
	ip := s.clientIP(r)

	if _, ok := lgexec.Lookup(cmd); !ok {
		http.Error(w, "unknown command", http.StatusBadRequest)
		return
	}

	ok, wait := s.lim.Allow(ip, ratelimit.Light)
	if !ok {
		http.Error(w, fmt.Sprintf("Rate limit exceeded. Try again in %s.", wait.Round(time.Second)), http.StatusTooManyRequests)
		return
	}
	release, ok := s.lim.Acquire(ip)
	if !ok {
		http.Error(w, "Another task is already running for your IP. Please wait.", http.StatusConflict)
		return
	}
	defer release()

	runner, err := lgexec.Run(r.Context(), cmd, target)
	if err != nil {
		http.Error(w, "Error: "+err.Error(), http.StatusBadRequest)
		return
	}

	h := w.Header()
	format := lgexec.FormatOf(cmd)
	// Structured trace output (JSON / raw pipe stream) must not be
	// HTML-escaped; the frontend parses it.
	if format != lgexec.FormatText {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("X-Output-Format", string(format))
	} else {
		h.Set("Content-Type", "text/plain; charset=utf-8")
	}
	h.Set("X-Accel-Buffering", "no")
	h.Set("Cache-Control", "no-store")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Plain-text commands get a shell-style echo line; structured formats do
	// not (the frontend renders its own header).
	if format == lgexec.FormatText {
		fmt.Fprintf(w, "$ %s %s\n\n", cmd, target)
		flusher.Flush()
	}

	for line := range runner.Lines {
		select {
		case <-r.Context().Done():
			runner.Kill()
			return
		default:
		}
		// Structured trace output must not be HTML-escaped; the frontend parses it.
		if format != lgexec.FormatText {
			fmt.Fprintln(w, line)
		} else {
			fmt.Fprintln(w, template.HTMLEscapeString(line))
		}
		flusher.Flush()
	}
}


func sseEscape(s string) string {
	return strings.ReplaceAll(s, "\n", "\\n")
}

// parseUnlockCLI parses the MediaUnlockTest CLI text output into structured JSON.
func parseUnlockCLI(raw string, lang i18n.Lang) *unlockCLIResult {
	// Strip ANSI, progress bars, headers.
	raw = regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(raw, "")
	raw = regexp.MustCompile(`\r[^\n]*\r`).ReplaceAllString(raw, "")

	type service struct {
		Name   string `json:"name"`
		Result string `json:"result"`
		Region string `json:"region"`
		Info   string `json:"info"`
	}
	type category struct {
		Name     string    `json:"category"`
		Services []service `json:"services"`
	}

	var cats []unlockCat
	var curCat *unlockCat
	var curSvcs []unlockSvc

	// Skip non-category lines: project info, IP details, interactive prompts
	skipRe := regexp.MustCompile(`^\[ ?[0-9]+\]|项目地址|使用方式|地区：|请选择|检测项目|取消检测|回车确认|检测完毕|当天运行|Made with|已经是最新版本|^\d+\.\d+\.\d+\.\d+$`)
	// IP info line: "IPv4 地址：..." or "ISP：..."
	infoRe := regexp.MustCompile(`^(IPv4|IPv6) 地址：|^ISP：|^地区：`)
	// Progress: "正在测试..."
	progRe := regexp.MustCompile(`正在测试|^\s*$`) 

	// Category header pattern: [ XXX (IPv4) ] or [ XX ]
	catRe := regexp.MustCompile(`^\[ (.+?) \(IPv4\) \]|^\[ (.+?) \]`)
	// Service pattern: Name    STATUS (extra)
	svcRe := regexp.MustCompile(`^(.{3,50}?)\s{2,}(YES|NO|Banned|ERR|Failed|Restricted|Unexpected)\b\s*(.*)`)

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || skipRe.MatchString(line) || infoRe.MatchString(line) || progRe.MatchString(line) {
			continue
		}
		// Category header
		if m := catRe.FindStringSubmatch(line); m != nil {
			if curCat != nil {
				curCat.Services = curSvcs
				cats = append(cats, *curCat)
			}
			name := m[1]
			if name == "" {
				name = m[2]
			}
			curCat = &unlockCat{Name: i18n.T(lang, catI18nKey(name))}
			curSvcs = nil
			continue
		}
		// Service line
		if m := svcRe.FindStringSubmatch(line); m != nil {
			name := strings.TrimSpace(m[1])
			status := m[2]
			extra := strings.TrimSpace(m[3])
			region := ""
			// Extract region from "Region: XX" or "(Region: XX)"
			if r := regexp.MustCompile(`Region:\s*(\S+)`).FindStringSubmatch(extra); r != nil {
				region = r[1]
			}
			curSvcs = append(curSvcs, unlockSvc{Name: name, Result: status, Region: region, Info: extra})
		}
	}
	if curCat != nil {
		curCat.Services = curSvcs
		cats = append(cats, *curCat)
	}

	return &unlockCLIResult{Categories: cats}
}

// catI18nKey maps CLI category headers to i18n keys.
func catI18nKey(name string) string {
	name = strings.TrimSpace(name)
	switch {
	case strings.EqualFold(name, "Globe") || strings.Contains(name, "跨国") || strings.Contains(name, "Global") || strings.Contains(name, "國際"):
		return "cat_global"
	case strings.EqualFold(name, "Taiwan") || strings.Contains(name, "台湾") || strings.Contains(name, "台灣"):
		return "cat_taiwan"
	case strings.EqualFold(name, "HongKong") || strings.Contains(name, "香港"):
		return "cat_hongkong"
	case strings.EqualFold(name, "Japan") || strings.Contains(name, "日本"):
		return "cat_japan"
	case strings.EqualFold(name, "Korea") || strings.Contains(name, "韩国") || strings.Contains(name, "韓國"):
		return "cat_korea"
	case strings.EqualFold(name, "NorthAmerica") || strings.Contains(name, "北美"):
		return "cat_na"
	case strings.EqualFold(name, "SouthAmerica") || strings.Contains(name, "南美"):
		return "cat_sa"
	case strings.EqualFold(name, "Europe") || strings.Contains(name, "欧洲") || strings.Contains(name, "歐洲"):
		return "cat_eu"
	case strings.EqualFold(name, "Africa") || strings.Contains(name, "非洲"):
		return "cat_africa"
	case strings.EqualFold(name, "SouthEastAsia") || strings.Contains(name, "东南亚") || strings.Contains(name, "東南亞"):
		return "cat_sea"
	case strings.EqualFold(name, "Oceania") || strings.Contains(name, "大洋洲"):
		return "cat_oceania"
	case strings.EqualFold(name, "AI") || strings.Contains(name, "ＡＩ"):
		return "cat_ai"
	}
	return name
}


type unlockSvc struct {
	Name   string `json:"name"`
	Result string `json:"result"`
	Region string `json:"region"`
	Info   string `json:"info"`
}
type unlockCat struct {
	Name     string      `json:"category"`
	Services []unlockSvc `json:"services"`
}
type unlockCLIResult struct {
	Categories []unlockCat `json:"categories"`
}

// handleFastTrace runs nexttrace --json against each configured target and
// streams results as SSE. Events:
//
//	event: target  data: {"name":"上海電信","host":"..."}     (before each target)
//	event: result  data: {"name":"...","host":"...","json":{...},"elapsed":1.2}
//	event: error   data: {"name":"...","host":"...","error":"..."}
//	event: done    data: {}
func (s *Server) handleFastTrace(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)

	ok, wait := s.lim.Allow(ip, ratelimit.Light)
	if !ok {
		http.Error(w, fmt.Sprintf("Rate limit exceeded. Try again in %s.", wait.Round(time.Second)), http.StatusTooManyRequests)
		return
	}
	release, ok := s.lim.Acquire(ip)
	if !ok {
		http.Error(w, "Another task is already running for your IP. Please wait.", http.StatusConflict)
		return
	}
	defer release()

	if len(s.cfg.FastTraceTargets) == 0 {
		http.Error(w, "fast_trace_targets not configured", http.StatusServiceUnavailable)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	fmt.Fprintf(w, "retry: 3000\n\n")
	flusher.Flush()

	for _, tgt := range s.cfg.FastTraceTargets {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Announce the target.
		meta, _ := json.Marshal(map[string]string{"name": tgt.Name, "host": tgt.Host})
		fmt.Fprintf(w, "event: target\ndata: %s\n\n", meta)
		flusher.Flush()

		start := time.Now()
		result, err := s.runTraceJSON(ctx, tgt.Host)
		el := time.Since(start).Seconds()

		if err != nil {
			errMsg, _ := json.Marshal(map[string]any{
				"name": tgt.Name, "host": tgt.Host, "error": err.Error(),
			})
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", errMsg)
		} else {
			payload, _ := json.Marshal(map[string]any{
				"name": tgt.Name, "host": tgt.Host,
				"json": json.RawMessage(result), "elapsed": el,
			})
			fmt.Fprintf(w, "event: result\ndata: %s\n\n", payload)
		}
		flusher.Flush()
	}

	fmt.Fprintf(w, "event: done\ndata: {}\n\n")
	flusher.Flush()
}

// runTraceJSON executes nexttrace --json for host and returns the raw JSON.
func (s *Server) runTraceJSON(ctx context.Context, host string) ([]byte, error) {
	runner, err := lgexec.Run(ctx, "traceroute", host)
	if err != nil {
		return nil, err
	}
	var buf strings.Builder
	for line := range runner.Lines {
		select {
		case <-ctx.Done():
			runner.Kill()
			return nil, ctx.Err()
		default:
		}
		buf.WriteString(line)
	}
	out := buf.String()
	// nexttrace prints a banner line before the JSON document; strip it.
	if i := strings.Index(out, "{"); i >= 0 {
		return []byte(out[i:]), nil
	}
		return nil, fmt.Errorf("no JSON output from nexttrace")
}

// handleUnlock runs the MediaUnlockTest CLI binary and returns parsed JSON.
// Shelling out guarantees 100% identical results to running the CLI directly.
func (s *Server) handleUnlock(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	ok, wait := s.lim.Allow(ip, ratelimit.Light)
	if !ok {
		http.Error(w, fmt.Sprintf("Rate limit exceeded. Try again in %s.", wait.Round(time.Second)), http.StatusTooManyRequests)
		return
	}
	release, ok := s.lim.Acquire(ip)
	if !ok {
		http.Error(w, "Another task is already running for your IP. Please wait.", http.StatusConflict)
		return
	}
	defer release()

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/usr/local/bin/unlock-test")
	cmd.Stdin = strings.NewReader("\n") // select all regions
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		http.Error(w, "Unlock test failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	result := parseUnlockCLI(string(out), i18n.DetectLang(r, i18n.Lang(s.cfg.DefaultLang)))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(result)
}

// handleDownload streams random bytes for speed testing.
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	if !s.checkSpeedToken(w, r) {
		return
	}
	sizeStr := strings.TrimPrefix(r.URL.Path, "/download/")
	size, err := parseSize(sizeStr)
	if err != nil || size <= 0 || size > 10<<30 {
		http.Error(w, "invalid size", http.StatusBadRequest)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Length", fmt.Sprintf("%d", size))
	h.Set("Content-Encoding", "identity")
	h.Set("Cache-Control", "no-store, no-cache, must-revalidate")
	h.Set("Accept-Ranges", "none")
	h.Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.test\"", sizeStr))

	flusher, _ := w.(http.Flusher)

	const chunk = 1 << 20 // 1 MiB
	buf := make([]byte, chunk)
	if _, err := rand.Read(buf); err != nil {
		http.Error(w, "entropy error", http.StatusInternalServerError)
		return
	}

	var sent int64
	for sent < size {
		select {
		case <-r.Context().Done():
			return
		default:
		}
		n := int64(chunk)
		if rem := size - sent; rem < n {
			n = rem
		}
		written, err := w.Write(buf[:n])
		sent += int64(written)
		if err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func parseSize(s string) (int64, error) {
	s = strings.ToLower(strings.TrimSuffix(s, ".test"))
	var mult int64 = 1
	switch {
	case strings.HasSuffix(s, "gb"):
		mult = 1 << 30
		s = strings.TrimSuffix(s, "gb")
	case strings.HasSuffix(s, "mb"):
		mult = 1 << 20
		s = strings.TrimSuffix(s, "mb")
	case strings.HasSuffix(s, "kb"):
		mult = 1 << 10
		s = strings.TrimSuffix(s, "kb")
	}
	var v int64
	_, err := fmt.Sscanf(s, "%d", &v)
	return v * mult, err
}

// handlePing is a lightweight endpoint for latency measurement. It returns
// an empty 204 immediately; the client measures round-trip time.
func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	if !s.checkSpeedToken(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// handleSpeedtestBegin consumes one heavy-rate token and issues a short-lived
// permit the client must present on subsequent ping/download/upload calls.
// This caps how often a single IP can run a full bandwidth test.
func (s *Server) handleSpeedtestBegin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	ip := s.clientIP(r)
	ok, wait := s.lim.Allow(ip, ratelimit.Heavy)
	if !ok {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Retry-After", fmt.Sprintf("%.0f", wait.Seconds()))
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":       "rate_limit",
			"retry_after": wait.Round(time.Second).String(),
		})
		return
	}
	tok := s.lim.IssueSpeedToken(ip)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"token": tok})
}

// checkSpeedToken verifies the X-Speedtest-Token header against the limiter.
// On failure it writes 403 and returns false.
func (s *Server) checkSpeedToken(w http.ResponseWriter, r *http.Request) bool {
	tok := r.Header.Get("X-Speedtest-Token")
	if tok == "" || !s.lim.HasSpeedToken(s.clientIP(r), tok) {
		http.Error(w, "speedtest token required (call /api/speedtest/begin first)", http.StatusForbidden)
		return false
	}
	return true
}

// handleUpload drains the request body and reports how many bytes were
// received and how long it took, so the client can compute upload speed.
// The payload is discarded.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !s.checkSpeedToken(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 10<<30) // 10 GB cap
	start := time.Now()
	buf := make([]byte, 64*1024)
	var total int64
	for {
		select {
		case <-r.Context().Done():
			return
		default:
		}
		n, err := r.Body.Read(buf)
		total += int64(n)
		if err != nil {
			break
		}
	}
	el := time.Since(start).Seconds()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"bytes":   total,
		"seconds": el,
		"mbps":    float64(total) * 8 / el / 1e6,
	})
}
