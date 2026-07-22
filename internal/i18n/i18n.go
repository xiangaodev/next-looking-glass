// Package i18n provides minimal internationalisation for the looking-glass
// UI.  Two languages are supported: Traditional Chinese (zh) and English (en).
// Translations are plain Go maps — no external files, no third-party deps.
package i18n

import (
	"net/http"
	"strings"
)

// Lang is a BCP-47 language tag used internally.
type Lang string

const (
	ZH Lang = "zh"
	EN Lang = "en"
)

// ---- translation map --------------------------------------------------------

var dict = map[Lang]map[string]string{
	ZH: zh,
	EN: en,
}

// T returns the translation for key in lang.  Falls back to key itself when
// a translation is missing (makes it obvious during development).
func T(lang Lang, key string) string {
	if m, ok := dict[lang]; ok {
		if v := m[key]; v != "" {
			return v
		}
	}
	// Fallback to English for missing keys, then to key itself.
	if lang != EN {
		if v := dict[EN][key]; v != "" {
			return v
		}
	}
	return key
}

// Map returns a shallow copy of the translation map for the given language so
// the frontend can access it via window.__I18N__.
func Map(lang Lang) map[string]string {
	src := dict[lang]
	if src == nil {
		src = dict[EN]
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// DetectLang picks the best UI language for a request.
// Priority: query ?lang=xx > cookie lang > Accept-Language > default.
func DetectLang(r *http.Request, defaultLang Lang) Lang {
	// 1. Query-string override.
	if q := r.URL.Query().Get("lang"); q != "" {
		if l := normalize(q); l != "" {
			return l
		}
	}
	// 2. Cookie.
	if c, err := r.Cookie("lang"); err == nil && c != nil {
		if l := normalize(c.Value); l != "" {
			return l
		}
	}
	// 3. Accept-Language header.
	if al := r.Header.Get("Accept-Language"); al != "" {
		for _, tag := range strings.Split(al, ",") {
			tag = strings.TrimSpace(tag)
			// Strip quality value: "zh-TW;q=0.9" → "zh-TW"
			if i := strings.IndexAny(tag, ";q="); i >= 0 {
				tag = tag[:i]
			}
			if l := normalize(tag); l != "" {
				return l
			}
		}
	}
	if defaultLang != "" {
		return defaultLang
	}
	return ZH
}

func normalize(s string) Lang {
	s = strings.TrimSpace(s)
	// Map common variations to our two languages.
	switch {
	case strings.HasPrefix(s, "zh"):
		return ZH
	case strings.HasPrefix(s, "en"):
		return EN
	}
	return ""
}

// ---- translation tables -----------------------------------------------------

var zh = map[string]string{
	// ---------- shell / layout ----------
	"site_desc":          "Nimbus Looking Glass — ping / traceroute / MTR / host / 下載測速 / 快速回程 / 串流解鎖",
	"nav_switch_nodes":   "節點切換",

	// ---------- meta strip ----------
	"meta_location":  "節點位置",
	"meta_ipv4":      "節點 IPv4",
	"meta_ipv6":      "節點 IPv6",
	"meta_your_ip":   "您的 IP",

	// ---------- sidebar ----------
	"tool_group_diag":  "網路診斷",
	"tool_group_ipv6":  "IPv6",
	"tool_group_speed": "測速",

	"tool_ping":         "Ping",
	"tool_ping6":        "Ping6",
	"tool_traceroute":   "Traceroute",
	"tool_traceroute6":  "Traceroute6",
	"tool_mtr":          "MTR",
	"tool_mtr6":         "MTR6",
	"tool_host":         "Host / DNS",
	"tool_speedtest":    "網路測速",
	"tool_fasttrace":    "快速回程",
	"tool_unlock":       "串流解鎖",

	// ---------- diag form ----------
	"label_target":     "目標主機 / IP 位址",
	"placeholder_host": "例如：google.com 或 1.1.1.1",
	"btn_run":          "開始測試",

	// ---------- trace table headers (traceroute) ----------
	"th_hop":      "跳",
	"th_ip":       "IP / 主機名",
	"th_rtt":      "延遲",
	"th_location": "位置",
	"th_asn":      "ASN / 運營商",

	// ---------- trace table headers (MTR) ----------
	"th_loss":  "丟失率",
	"th_mtr_rtt": "延遲 (最好~最差, σ)",

	// ---------- fast trace ----------
	"fasttrace_title": "快速回程測試",
	"fasttrace_desc":  "伺服器對中國大陸三大 ISP 核心節點進行 traceroute，快速判斷回程路由品質。",
	"ft_powered_by": "基於",
	"fasttrace_empty": "點擊下方按鈕開始測試",
	"btn_fasttrace":   "開始快速回程測試",

	// ---------- speedtest ----------
	"speedtest_ready":  "準備就緒",
	"btn_speedtest":    "開始測速",
	"stat_download":    "下載",
	"stat_upload":      "上傳",
	"stat_ping":        "延遲",
	"stat_jitter":      "抖動",
	"speedtest_hint":   "點擊「開始測速」測量您到此節點的下載、上傳與延遲。全程約 25 秒。",

	// ---------- unlock ----------
	"unlock_title": "串流媒體解鎖檢測",
	"unlock_desc":  "檢測此節點對主流串流平台的地區解鎖狀態（Netflix / YouTube / Disney+ / ChatGPT 等）。全程約 30–60 秒。",
	"unlock_disclaimer": "檢測結果僅供參考，實際情況可能因 IP 變動、平台政策等因素有所不同。",
	"btn_unlock":   "開始檢測",

	// ---------- terminal ----------
	"btn_stop":     "停止",
	"credits_text": "路由追蹤 by nexttrace · 串流檢測 by MediaUnlockTest",

	// ---------- JS: CMD hints ----------
	"cmd_hint_ping":   "ping -4 -c 4",
	"cmd_hint_ping6":  "ping -6 -c 4",
	"cmd_hint_trace":  "nexttrace (路由追蹤 + 地理位置)",
	"cmd_hint_trace6": "nexttrace -6 (路由追蹤 + 地理位置)",
	"cmd_hint_mtr":    "nexttrace --mtr --report (路由統計)",
	"cmd_hint_mtr6":   "nexttrace -6 --mtr --report (路由統計)",
	"cmd_hint_host":   "host",
	"cmd_hint_prefix": "指令：",

	// ---------- JS: errors ----------
	"err_prefix":       "錯誤",
	"err_http":         "錯誤 %d: %s",
	"err_connect":      "連線錯誤: %s",
	"err_no_output":    "未取得有效的 nexttrace 輸出",
	"err_parse_fail":   "解析失敗: %s",
	"err_unlock":       "錯誤: %s",

	// ---------- JS: trace (traceroute) ----------
	"trace_tracing":  "正在追蹤到 %s 的路由…",
	"trace_done":     "追蹤完成：%d 跳 · 耗時 %s 秒 · 目標 %s",
	"trace_cancelled": "已取消。",

	// ---------- JS: MTR ----------
	"mtr_tracing":  "MTR 統計中（目標 %s）…",
	"mtr_done":     "MTR 完成：%d 跳 · 耗時 %s 秒 · 目標 %s",

	// ---------- JS: fast trace ----------
	"ft_tracing":     "正在追蹤 %s…",
	"ft_connecting":  "正在連線…",
	"ft_elapsed":     "耗時 %s 秒",
	"ft_all_done":    "全部完成 · 耗時 %s 秒",
	"ft_lost":        " (連線中斷)",
	"ft_error":       "錯誤: %s",

	// ---------- JS: trace table ----------
	"trace_no_response": "* * * 無回應",

	// ---------- JS: unlock ----------
	"unlock_loading":       "正在檢測串流解鎖狀態，請稍候…",
	"unlock_items":         "%d 項",
	"unlock_footer":        "IPv4: %s · ISP: %s",

	// ---------- JS: speedtest ----------
	"sp_acquire":         "取得測速許可…",
	"sp_latency":         "測量延遲…",
	"sp_download":        "下載測速中…",
	"sp_upload":          "上傳測速中…",
	"sp_done":            "測速完成",
	"sp_ready":           "就緒",
	"sp_done_msg":        "完成於 %s · 下載 %s Mbps · 上傳 %s Mbps · 延遲 %s ms",
	"sp_cancelled":       "測速已取消。",
	"sp_failed":          "測速失敗：%s",
	"sp_rate_limit":      "測速次數已達上限，請於 %s 再試",
	"sp_rate_limit_later": "稍後",
	"sp_permit_fail":     "無法取得測速許可 (HTTP %d)",
	"sp_running":         "測速進行中，請勿關閉頁面…",

	// ---------- unlock categories ----------
	"cat_global":    "國際平台",
	"cat_taiwan":    "台灣平台",
	"cat_hongkong":  "香港平台",
	"cat_japan":     "日本平台",
	"cat_korea":     "韓國平台",
	"cat_na":        "北美平台",
	"cat_sa":        "南美平台",
	"cat_eu":        "歐洲平台",
	"cat_africa":    "非洲平台",
	"cat_sea":       "東南亞平台",
	"cat_oceania":   "大洋洲平台",
	"cat_ai":        "ＡＩ平台",
}

var en = map[string]string{
	// ---------- shell / layout ----------
	"site_desc":          "Nimbus Looking Glass — ping / traceroute / MTR / host / speed test / fast trace / unlock",
	"nav_switch_nodes":   "Switch Node",

	// ---------- meta strip ----------
	"meta_location":  "Location",
	"meta_ipv4":      "Node IPv4",
	"meta_ipv6":      "Node IPv6",
	"meta_your_ip":   "Your IP",

	// ---------- sidebar ----------
	"tool_group_diag":  "Diagnostics",
	"tool_group_ipv6":  "IPv6",
	"tool_group_speed": "Speed",

	"tool_ping":         "Ping",
	"tool_ping6":        "Ping6",
	"tool_traceroute":   "Traceroute",
	"tool_traceroute6":  "Traceroute6",
	"tool_mtr":          "MTR",
	"tool_mtr6":         "MTR6",
	"tool_host":         "Host / DNS",
	"tool_speedtest":    "Speed Test",
	"tool_fasttrace":    "Fast Trace",
	"tool_unlock":       "Unlock Test",

	// ---------- diag form ----------
	"label_target":     "Target Host / IP",
	"placeholder_host": "e.g. google.com or 1.1.1.1",
	"btn_run":          "Run",

	// ---------- trace table headers (traceroute) ----------
	"th_hop":      "Hop",
	"th_ip":       "IP / Hostname",
	"th_rtt":      "Latency",
	"th_location": "Location",
	"th_asn":      "ASN / Provider",

	// ---------- trace table headers (MTR) ----------
	"th_loss":     "Loss%",
	"th_mtr_rtt":  "Latency (Best~Worst, σ)",

	// ---------- fast trace ----------
	"fasttrace_title": "Fast Trace",
	"fasttrace_desc":  "Traceroute to mainland China ISPs (Shanghai Telecom / Beijing Unicom / Guangzhou Mobile).",
	"ft_powered_by": "Powered by",
	"fasttrace_empty": "Click the button below to start",
	"btn_fasttrace":   "Start Fast Trace",

	// ---------- speedtest ----------
	"speedtest_ready":  "Ready",
	"btn_speedtest":    "Start Test",
	"stat_download":    "Download",
	"stat_upload":      "Upload",
	"stat_ping":        "Ping",
	"stat_jitter":      "Jitter",
	"speedtest_hint":   "Click \"Start Test\" to measure download, upload and latency to this node. Takes ~25 seconds.",

	// ---------- unlock ----------
	"unlock_title": "Streaming Unlock Test",
	"unlock_desc":  "Checks region-unlock status for major streaming platforms (Netflix / YouTube / Disney+ / ChatGPT etc.). Takes ~30–60 seconds.",
	"unlock_disclaimer": "Results are for reference only. Actual availability may vary due to IP changes, platform policies, etc.",
	"btn_unlock":   "Start Test",

	// ---------- terminal ----------
	"btn_stop":     "Stop",
	"credits_text": "Routing by nexttrace · Unlock test by MediaUnlockTest",

	// ---------- JS: CMD hints ----------
	"cmd_hint_ping":   "ping -4 -c 4",
	"cmd_hint_ping6":  "ping -6 -c 4",
	"cmd_hint_trace":  "nexttrace (route tracing + GeoIP)",
	"cmd_hint_trace6": "nexttrace -6 (route tracing + GeoIP)",
	"cmd_hint_mtr":    "nexttrace --mtr --report (route stats)",
	"cmd_hint_mtr6":   "nexttrace -6 --mtr --report (route stats)",
	"cmd_hint_host":   "host",
	"cmd_hint_prefix": "Command: ",

	// ---------- JS: errors ----------
	"err_prefix":       "Error",
	"err_http":         "Error %d: %s",
	"err_connect":      "Connection error: %s",
	"err_no_output":    "No valid nexttrace output",
	"err_parse_fail":   "Parse failed: %s",
	"err_unlock":       "Error: %s",

	// ---------- JS: trace (traceroute) ----------
	"trace_tracing":    "Tracing route to %s…",
	"trace_done":       "Trace done: %d hops · took %s sec · target %s",
	"trace_cancelled":  "Cancelled.",

	// ---------- JS: MTR ----------
	"mtr_tracing":  "MTR stats for %s…",
	"mtr_done":     "MTR done: %d hops · took %s sec · target %s",

	// ---------- JS: fast trace ----------
	"ft_tracing":     "Tracing %s…",
	"ft_connecting":  "Connecting…",
	"ft_elapsed":     "%s sec elapsed",
	"ft_all_done":    "All done · %s sec total",
	"ft_lost":        " (disconnected)",
	"ft_error":       "Error: %s",

	// ---------- JS: trace table ----------
	"trace_no_response": "* * * no response",

	// ---------- JS: unlock ----------
	"unlock_loading":       "Checking streaming unlock status, please wait…",
	"unlock_items":         "%d items",
	"unlock_footer":        "IPv4: %s · ISP: %s",

	// ---------- JS: speedtest ----------
	"sp_acquire":         "Acquiring permit…",
	"sp_latency":         "Measuring latency…",
	"sp_download":        "Testing download…",
	"sp_upload":          "Testing upload…",
	"sp_done":            "Test complete",
	"sp_ready":           "Ready",
	"sp_done_msg":        "Done at %s · Download %s Mbps · Upload %s Mbps · Ping %s ms",
	"sp_cancelled":       "Test cancelled.",
	"sp_failed":          "Test failed: %s",
	"sp_rate_limit":      "Rate limit reached, try again in %s",
	"sp_rate_limit_later": "a moment",
	"sp_permit_fail":     "Unable to acquire speedtest permit (HTTP %d)",
	"sp_running":         "Test in progress, do not close the page…",

	// ---------- unlock categories ----------
	"cat_global":    "Global",
	"cat_taiwan":    "Taiwan",
	"cat_hongkong":  "Hong Kong",
	"cat_japan":     "Japan",
	"cat_korea":     "Korea",
	"cat_na":        "North America",
	"cat_sa":        "South America",
	"cat_eu":        "Europe",
	"cat_africa":    "Africa",
	"cat_sea":       "Southeast Asia",
	"cat_oceania":   "Oceania",
	"cat_ai":        "AI",
}
