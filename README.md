# Next Looking Glass

[![Go Version](https://img.shields.io/badge/Go-1.26.4-00ADD8)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

現代化的 Looking Glass 網路診斷工具 — [筋斗雲 (nimbus.com.tw)](https://www.nimbus.com.tw) 專用。Go 單一二進位、內嵌前端、YAML 設定，取代舊版 PHP/jQuery 實作。

## 功能

- **Ping / Ping6** — 即時流式輸出
- **路由追蹤（nexttrace）** — traceroute / MTR 皆由 nexttrace 驅動，自動顯示每跳地理位置、ASN、運營商
- **快速回程** — 一鍵檢測三大中國 ISP（上海電信/北京聯通/廣州移動）回程路由
- **Host** — DNS 查詢
- **網路測速** — speedtest.net 風格儀表盤：下載 / 上傳 / 延遲 / 抖動
- **串流解鎖** — 檢測超過 180 項串流平台的區域解鎖狀態（Netflix / YouTube / Disney+ / ChatGPT 等）

## 安全設計

- 指令白名單 + `exec.CommandContext` 直接傳參（無 shell，杜絕注入）
- 目標校驗：IPv4/IPv6/網域，**雙棧皆拒絕內網/環回/保留段**（修復舊版 IPv6 漏點）
- 每 IP 分級限速：輕量指令 30 次/小時、測速 3 次/小時；每 IP 同時僅 1 個任務
- 進程組管理：逾時/斷線 kill 整個進程組，無殭屍行程
- 前端僅用 `textContent` 渲染輸出，XSS 免疫；嚴格 CSP / X-Frame-Options / nosniff

## 快速開始

### 前置需求

伺服器需安裝系統指令、**nexttrace**（路由追蹤）以及 **unlock-test**（串流解鎖檢測；`/api/unlock` 會 shell-out 呼叫它）：

```bash
# Debian / Ubuntu
apt-get install -y iputils-ping dnsutils curl

# RHEL / Rocky / Alma
dnf install -y iputils bind-utils curl

# nexttrace（路由追蹤 + 地理位置）
curl -fsSL -o /usr/local/bin/nexttrace \
  https://github.com/nxtrace/NTrace-core/releases/download/v1.7.1/nexttrace_linux_amd64
chmod +x /usr/local/bin/nexttrace

# unlock-test（串流解鎖 — 由 MediaUnlockTest 提供的獨立 CLI 二進位）
curl -fsSL -o /usr/local/bin/unlock-test \
  https://github.com/HsukqiLee/MediaUnlockTest/releases/latest/download/unlock-test_linux_amd64
chmod +x /usr/local/bin/unlock-test
```

> 也可以直接用 Ansible 一鍵部署所有節點，見 `deploy/README.md`（playbook 會自動下載 nexttrace 與 unlock-test）。

### 本機開發

```bash
go mod tidy
make run          # 編譯並以 config.yaml 啟動於 :8080
```

### 部署到節點（Ansible）

見 `deploy/README.md`。一鍵部署 7 個節點，Apache 反代到 Go 後端，零停機可回滾。



### Docker

```bash
docker build -t nimbus/next-looking-glass .
docker run -d --name lg \
  -p 8080:8080 \
  -v $(pwd)/config.yaml:/app/config.yaml:ro \
  --cap-add NET_RAW \
  nimbus/next-looking-glass
```

### 反向代理（Caddy 範例）

```
lg-tw-g.nimbus.com.tw {
    reverse_proxy 127.0.0.1:8080
    header_down X-Accel-Buffering no
}
```

設定檔記得把 `trust_proxy: true` 打開，讓限速器看到真實訪客 IP。

## 設定說明

見 `config.yaml` 註解。每個節點各自維護一份設定，只改 `server_location`、
`ipv4`、`ipv6` 即可；`nodes` 區塊所有節點保持一致。

## API

| 端點 | 說明 |
|---|---|
| `GET /` | 首頁 |
| `GET /api/info` | 節點資訊 JSON |
| `GET /api/diag?cmd=ping&target=1.1.1.1` | 流式診斷輸出 |
| `GET /api/bench` | SSE 體檢事件流 |
| `GET /download/{10mb,100mb,1gb}` | 隨機位元組測速檔 |

## 授權

MIT License — 見 [LICENSE](LICENSE)。

GitHub: [github.com/xiangaodev/next-looking-glass](https://github.com/xiangaodev/next-looking-glass)
