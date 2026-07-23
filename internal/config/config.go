package config

import (
	"errors"
	"fmt"
	"html/template"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Node describes a remote looking-glass node shown in the top navigation.
type Node struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

// SpeedtestNode is a remote HTTP endpoint that can serve a download
// payload for distributed speed tests.
type SpeedtestNode struct {
	Name string `yaml:"name"` // e.g. "Taipei, TW"
	URL  string `yaml:"url"`  // e.g. "https://speedtest.tpe.example.com/100mb.test"
}

// FastTraceTarget is a preset traceroute destination for the one-click
// multi-ISP route test.
type FastTraceTarget struct {
	Name string `yaml:"name"` // e.g. "上海電信"
	Host string `yaml:"host"` // e.g. "ipv4.sha-4134.endpoint.nxtrace.org"
}

// RateLimitConfig controls per-IP token buckets.
type RateLimitConfig struct {
	LightPerHour int `yaml:"light_per_hour"` // ping/traceroute/mtr/host
	HeavyPerHour int `yaml:"heavy_per_hour"` // speedtest
}

// Config is the root YAML configuration.
type Config struct {
	Listen           string            `yaml:"listen"`
	SiteName         string            `yaml:"site_name"`
	SiteURL          string            `yaml:"site_url"`
	ServerLocation   string            `yaml:"server_location"`
	IPv4             string            `yaml:"ipv4"`
	IPv6             string            `yaml:"ipv6"`
	TestFiles        []string          `yaml:"test_files"` // e.g. ["10MB","100MB","1GB"]
	Nodes            []Node            `yaml:"nodes"`
	SpeedtestNodes   []SpeedtestNode   `yaml:"speedtest_nodes"`
	FastTraceTargets []FastTraceTarget `yaml:"fast_trace_targets"`
	RateLimit        RateLimitConfig   `yaml:"rate_limit"`
	TrustProxy       bool              `yaml:"trust_proxy"`
	DefaultLang      string            `yaml:"default_lang"`   // "zh-Hant" / "zh-Hans" / "en", default "zh-Hant"
	LogoText         string            `yaml:"logo_text"`      // text in brand-mark, default first char of site_name
	LogoURL          string            `yaml:"logo_url"`       // if set, replaces brand-mark with <img>
	LogoBase64       string            `yaml:"logo_base64"`    // raw base64 image (derives data: URI)
	FaviconURL       string            `yaml:"favicon_url"`    // if set, replaces auto-generated SVG favicon
	FaviconBase64    string            `yaml:"favicon_base64"` // raw base64 favicon image
}

// LogoChar returns the one-character string used for the brand-mark and
// favicon when no custom logo URL is provided.
func (c *Config) LogoChar() string {
	if c.LogoText != "" {
		// Take at most 2 chars for CJK logos.
		r := []rune(c.LogoText)
		if len(r) > 2 {
			return string(r[:2])
		}
		return c.LogoText
	}
	if c.SiteName != "" {
		return string([]rune(c.SiteName)[:1])
	}
	return "N"
}

// LogoSrc returns the effective logo src. Priority: LogoURL > LogoBase64 > empty.
// LogoBase64 is auto-wrapped as a data: URI with MIME detection.
func (c *Config) LogoSrc() template.URL {
	if c.LogoURL != "" {
		return template.URL(c.LogoURL)
	}
	if c.LogoBase64 != "" {
		mime := "image/png"
		s := strings.TrimSpace(c.LogoBase64)
		if len(s) > 20 {
			switch {
			case strings.HasPrefix(s, "/9j/"):
				mime = "image/jpeg"
			case strings.HasPrefix(s, "R0lGOD"):
				mime = "image/gif"
			case strings.HasPrefix(s, "PHN2Zy"), strings.HasPrefix(s, "PD94bW"):
				mime = "image/svg+xml"
			}
		}
		return template.URL("data:" + mime + ";base64," + s)
	}
	return ""
}

// FaviconSrc returns the effective favicon src. Priority: FaviconURL > FaviconBase64 > empty.
func (c *Config) FaviconSrc() template.URL {
	if c.FaviconURL != "" {
		return template.URL(c.FaviconURL)
	}
	if c.FaviconBase64 != "" {
		mime := "image/png"
		s := strings.TrimSpace(c.FaviconBase64)
		if len(s) > 20 {
			switch {
			case strings.HasPrefix(s, "/9j/"):
				mime = "image/jpeg"
			case strings.HasPrefix(s, "R0lGOD"):
				mime = "image/gif"
			case strings.HasPrefix(s, "PHN2Zy"), strings.HasPrefix(s, "PD94bW"):
				mime = "image/svg+xml"
			}
		}
		return template.URL("data:" + mime + ";base64," + s)
	}
	return ""
}

func defaults() Config {
	return Config{
		Listen:   ":8080",
		SiteName: "Nimbus Looking Glass",
		SiteURL:  "https://www.nimbus.com.tw",
		TestFiles: []string{
			"10MB", "100MB", "1GB",
		},
		RateLimit: RateLimitConfig{
			LightPerHour: 30,
			HeavyPerHour: 3,
		},
	}
}

// Load reads and validates the YAML config at path.
func Load(path string) (*Config, error) {
	cfg := defaults()

	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate enforces required fields and normalises values.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Listen) == "" {
		return errors.New("listen must not be empty")
	}
	if strings.TrimSpace(c.ServerLocation) == "" {
		return errors.New("server_location must not be empty")
	}
	if c.RateLimit.LightPerHour < 0 || c.RateLimit.HeavyPerHour < 0 {
		return errors.New("rate_limit values must be >= 0")
	}
	for i, n := range c.Nodes {
		if n.Name == "" || n.URL == "" {
			return fmt.Errorf("nodes[%d] requires name and url", i)
		}
	}
	for i, n := range c.SpeedtestNodes {
		if n.Name == "" || n.URL == "" {
			return fmt.Errorf("speedtest_nodes[%d] requires name and url", i)
		}
	}
	for i, t := range c.FastTraceTargets {
		if t.Name == "" || t.Host == "" {
			return fmt.Errorf("fast_trace_targets[%d] requires name and host", i)
		}
	}
	return nil
}
