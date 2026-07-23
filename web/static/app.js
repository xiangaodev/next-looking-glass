/* Nimbus Next Looking Glass — frontend logic.
 * All user-visible strings use window.__I18N__ injected by the server.
 * i18n("key") or i18n("key", arg1, arg2) for printf-style formatting. */
(() => {
  "use strict";

  const $ = (sel) => document.querySelector(sel);
  const $$ = (sel) => Array.from(document.querySelectorAll(sel));

  /* i18n helper */
  const i18n = (key, ...args) => {
    let s = (window.__I18N__ && window.__I18N__[key]) || key;
    args.forEach((a) => { s = s.replace("%s", a).replace("%d", a); });
    return s;
  };

  const panels = {
    diag: $("#diagPanel"),
    speedtest: $("#speedtestPanel"),
  };
  const terminalWrap = $("#terminalWrap");
  const terminal = $("#terminal");
  const terminalTitle = $("#terminalTitle");
  const stopBtn = $("#stopBtn");
  const runBtn = $("#runBtn");
  const cmdHint = $("#cmdHint");
  const diagForm = $("#diagForm");
  const targetInput = $("#target");

  let currentTool = "ping";
  let abortCtl = null;

  const CMD_HINTS = {
    ping:       () => i18n("cmd_hint_ping"),
    ping6:      () => i18n("cmd_hint_ping6"),
    traceroute: () => i18n("cmd_hint_trace"),
    traceroute6:() => i18n("cmd_hint_trace6"),
    mtr:        () => i18n("cmd_hint_mtr"),
    mtr6:       () => i18n("cmd_hint_mtr6"),
    host:       () => i18n("cmd_hint_host"),
  };

  const TRACE_CMDS = new Set(["traceroute", "traceroute6", "mtr", "mtr6"]);

  /* ---------- per-tool URL routing ----------
   * Each tool has a permanent /tools/{slug} URL so it can be linked, bookmarked
   * and shared. Switching tools via the sidebar updates history.pushState; the
   * popstate handler keeps the UI in sync with browser back/forward.
   * The server emits the initial tool via <body data-initial-tool> so deep
   * links land directly on the right panel. */
  const VALID_SLUGS = new Set([
    "ping", "traceroute", "mtr", "host",
    "ping6", "traceroute6", "mtr6",
    "speedtest", "fasttrace", "unlock",
  ]);

  function slugFromPath(path) {
    if (!path.startsWith("/tools/")) return null;
    const slug = path.slice("/tools/".length);
    // Reject empty, nested, or non-canonical paths; serve 404-equivalent.
    if (!slug || slug.includes("/")) return null;
    return VALID_SLUGS.has(slug) ? slug : null;
  }

  function currentSlugFromURL() {
    return slugFromPath(location.pathname);
  }

  /* Switch to a tool programmatically: highlight sidebar, run switchPanel,
   * and push a history entry unless we're already on that URL. */
  function activateTool(tool, opts) {
    const push = !opts || opts.push !== false;
    $$(".tool-btn").forEach((b) => b.classList.toggle("active", b.dataset.tool === tool));
    currentTool = tool;
    switchPanel(tool);
    if (push) {
      const target = "/tools/" + tool;
      if (location.pathname !== target) {
        history.pushState({ tool }, "", target);
      }
    }
  }

  /* ---------- node navigation ---------- */
  $("#nodeSelect").addEventListener("change", (e) => {
    if (e.target.value) location.href = e.target.value;
  });

  /* ---------- tool switching ---------- */

  $$(".tool-btn").forEach((btn) => {
    btn.addEventListener("click", () => {
      activateTool(btn.dataset.tool);
    });
  });

  /* Sync UI with browser back/forward without reloading. */
  window.addEventListener("popstate", () => {
    const slug = currentSlugFromURL();
    if (slug && slug !== currentTool) activateTool(slug, { push: false });
  });

  function switchPanel(tool) {
    stopRunning();
    const isDiag = tool in CMD_HINTS;
    panels.diag.classList.toggle("hidden", !isDiag);
    panels.speedtest.classList.toggle("hidden", tool !== "speedtest");
    terminalWrap.classList.add("hidden");
    $("#tracePanel").classList.add("hidden");
    $("#fasttracePanel").classList.add("hidden");
    $("#unlockPanel").classList.add("hidden");
    if (tool === "fasttrace") {
      $("#fasttracePanel").classList.remove("hidden");
      initFtTabs();
    }
    if (tool === "unlock") {
      $("#unlockPanel").classList.remove("hidden");
    }
    if (isDiag) cmdHint.textContent = i18n("cmd_hint_prefix") + CMD_HINTS[tool]();
  }

  /* ---------- streaming diagnostics ---------- */

  diagForm.addEventListener("submit", async (e) => {
    e.preventDefault();
    const target = targetInput.value.trim();
    if (!target) return;

    if (TRACE_CMDS.has(currentTool)) {
      await runTrace(target);
    } else {
      await runDiag(target);
    }
  });

  async function runDiag(target) {
    startTerminal(`${currentTool} ${target}`);
    runBtn.disabled = true;
    abortCtl = new AbortController();

    try {
      const url = `/api/diag?cmd=${encodeURIComponent(currentTool)}&target=${encodeURIComponent(target)}`;
      const resp = await fetch(url, { signal: abortCtl.signal });
      if (!resp.ok) {
        appendLine(i18n("err_http", resp.status, await resp.text()), "err");
        return;
      }
      await streamResponse(resp);
    } catch (err) {
      if (err.name !== "AbortError") appendLine(i18n("err_connect", err.message), "err");
    } finally {
      finishTerminal();
      runBtn.disabled = false;
    }
  }

  async function streamResponse(resp) {
    const reader = resp.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split("\n");
      buffer = lines.pop();
      for (const line of lines) appendLine(line);
    }
    if (buffer) appendLine(buffer);
  }

  /* ---------- nexttrace entry ---------- */

  async function runTrace(target) {
    const isMtr = currentTool.startsWith("mtr");
    setTraceHeader(isMtr);
    if (isMtr) {
      await runMtrTrace(target);
    } else {
      await runJsonTrace(target);
    }
  }

  function setTraceHeader(isMtr) {
    const head = $("#traceHead");
    head.innerHTML = isMtr
      ? `<tr><th>${i18n("th_hop")}</th><th>${i18n("th_ip")}</th><th>${i18n("th_loss")}</th><th>${i18n("th_mtr_rtt")}</th><th>${i18n("th_location")}</th><th>${i18n("th_asn")}</th></tr>`
      : `<tr><th>${i18n("th_hop")}</th><th>${i18n("th_ip")}</th><th>${i18n("th_rtt")}</th><th>${i18n("th_location")}</th><th>${i18n("th_asn")}</th></tr>`;
  }

  /* --- traceroute JSON --- */
  async function runJsonTrace(target) {
    const tracePanel = $("#tracePanel"), traceBody = $("#traceBody");
    terminalWrap.classList.add("hidden");
    tracePanel.classList.remove("hidden");
    traceBody.innerHTML = "";
    setTraceStatus(i18n("trace_tracing", escapeHtml(target)));
    runBtn.disabled = true;
    abortCtl = new AbortController();

    const t0 = performance.now();
    try {
      const url = `/api/diag?cmd=${encodeURIComponent(currentTool)}&target=${encodeURIComponent(target)}`;
      const resp = await fetch(url, { signal: abortCtl.signal });
      if (!resp.ok) {
        setTraceStatus(i18n("err_http", resp.status, escapeHtml(await resp.text())));
        return;
      }
      const text = await resp.text();
      const jsonStart = text.indexOf("{");
      if (jsonStart < 0) throw new Error(i18n("err_no_output"));
      const data = JSON.parse(text.slice(jsonStart));
      renderJsonTrace(data, traceBody);
      const el = ((performance.now() - t0) / 1000).toFixed(1);
      const hops = (data.Hops || []).length;
      setTraceStatus(i18n("trace_done", hops, el, escapeHtml(target)));
    } catch (err) {
      if (err.name === "AbortError") setTraceStatus(i18n("trace_cancelled"));
      else setTraceStatus(i18n("err_parse_fail", escapeHtml(err.message)));
    } finally {
      runBtn.disabled = false;
      abortCtl = null;
    }
  }

  /* --- MTR raw stream --- */
  async function runMtrTrace(target) {
    const tracePanel = $("#tracePanel"), traceBody = $("#traceBody");
    terminalWrap.classList.add("hidden");
    tracePanel.classList.remove("hidden");
    traceBody.innerHTML = "";
    setTraceStatus(i18n("mtr_tracing", escapeHtml(target)));
    runBtn.disabled = true;
    abortCtl = new AbortController();

    const hopsByTtl = new Map();
    const t0 = performance.now();

    try {
      const url = `/api/diag?cmd=${encodeURIComponent(currentTool)}&target=${encodeURIComponent(target)}`;
      const resp = await fetch(url, { signal: abortCtl.signal });
      if (!resp.ok) {
        setTraceStatus(i18n("err_http", resp.status, escapeHtml(await resp.text())));
        return;
      }

      const reader = resp.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split("\n");
        buffer = lines.pop();
        for (const line of lines) {
          if (ingestMtrLine(line, hopsByTtl)) renderMtrTable(hopsByTtl, traceBody);
        }
      }
      if (buffer) {
        if (ingestMtrLine(buffer, hopsByTtl)) renderMtrTable(hopsByTtl, traceBody);
      }

      const el = ((performance.now() - t0) / 1000).toFixed(1);
      setTraceStatus(i18n("mtr_done", hopsByTtl.size, el, escapeHtml(target)));
    } catch (err) {
      if (err.name === "AbortError") setTraceStatus(i18n("trace_cancelled"));
      else setTraceStatus(i18n("ft_error", escapeHtml(err.message)));
    } finally {
      runBtn.disabled = false;
      abortCtl = null;
    }
  }

  function ingestMtrLine(line, hopsByTtl) {
    line = line.trim();
    if (!line || !line.includes("|")) return false;
    line = line.replace(/\x1b\[[0-9;]*m/g, "");
    const f = line.split("|");
    if (f.length < 4) return false;
    const ttl = parseInt(f[0], 10);
    if (!Number.isFinite(ttl) || ttl <= 0) return false;

    let hop = hopsByTtl.get(ttl);
    if (!hop) {
      hop = { ttl, ip: "", asn: "", country: "", prov: "", city: "", owner: "", sent: 0, recv: 0, rtts: [] };
      hopsByTtl.set(ttl, hop);
    }
    hop.sent++;
    const ip = f[1];
    if (ip && ip !== "*") {
      hop.recv++;
      hop.ip = ip;
      const rtt = parseFloat(f[3]);
      if (Number.isFinite(rtt)) hop.rtts.push(rtt);
      if (f[4]) hop.asn = f[4];
      if (f[5]) hop.country = f[5];
      if (f[6]) hop.prov = f[6];
      if (f[7]) hop.city = f[7];
      if (f[9]) hop.owner = f[9];
    }
    return true;
  }

  function renderMtrTable(hopsByTtl, tbody) {
    const hops = Array.from(hopsByTtl.values()).sort((a, b) => a.ttl - b.ttl);
    tbody.innerHTML = "";
    for (const hop of hops) {
      const tr = document.createElement("tr");

      const tdHop = td("trace-hop", hop.ttl); tr.appendChild(tdHop);
      const tdIp = td(""); tdIp.appendChild(el("span", "trace-ip", hop.ip || "*")); tr.appendChild(tdIp);

      const loss = hop.sent > 0 ? ((hop.sent - hop.recv) / hop.sent) * 100 : 0;
      tr.appendChild(td("trace-rtt" + (loss > 0 ? " slow" : ""), loss.toFixed(0) + "%"));

      const tdRtt = document.createElement("td");
      if (hop.rtts.length) {
        const avg = hop.rtts.reduce((a, b) => a + b, 0) / hop.rtts.length;
        const best = Math.min(...hop.rtts);
        const wrst = Math.max(...hop.rtts);
        const stdev = Math.sqrt(hop.rtts.reduce((s, v) => s + (v - avg) ** 2, 0) / hop.rtts.length);
        tdRtt.className = "trace-rtt" + (avg < 50 ? " fast" : avg > 200 ? " slow" : "");
        tdRtt.textContent = `${avg.toFixed(1)} (${best.toFixed(1)}~${wrst.toFixed(1)}, σ${stdev.toFixed(1)})`;
      } else { tdRtt.textContent = "-"; }
      tr.appendChild(tdRtt);

      const locParts = []; if (hop.country) locParts.push(hop.country); if (hop.prov && hop.prov !== hop.country) locParts.push(hop.prov); if (hop.city) locParts.push(hop.city);
      tr.appendChild(td("trace-loc", locParts.join(" ") || "-"));

      const asnParts = []; if (hop.asn && hop.asn !== "RFC1918") asnParts.push("AS" + hop.asn); if (hop.owner) asnParts.push(hop.owner);
      tr.appendChild(td("trace-asn", asnParts.join(" · ") || "-"));

      tbody.appendChild(tr);
    }
  }

  function td(cls, txt) { const d = document.createElement("td"); if (cls) d.className = cls; if (txt != null) d.textContent = txt; return d; }
  function el(tag, cls, txt) { const e = document.createElement(tag); if (cls) e.className = cls; if (txt != null) e.textContent = txt; return e; }

  /* --- traceroute JSON table --- */
  function renderJsonTrace(data, tbody) {
    const hops = data.Hops || [];
    tbody.innerHTML = "";
    for (let i = 0; i < hops.length; i++) {
      const samples = hops[i];
      const tr = document.createElement("tr");
      const ttl = samples[0] && samples[0].TTL != null ? samples[0].TTL : i + 1;
      tr.appendChild(td("trace-hop", ttl));

      const ok = samples.find((s) => s.Success && s.Address && s.Address.IP);
      if (!ok) {
        const t = document.createElement("td"); t.colSpan = 4; t.className = "trace-timeout"; t.textContent = i18n("trace_no_response"); tr.appendChild(t);
        tbody.appendChild(tr); continue;
      }

      const tdIp = document.createElement("td");
      tdIp.appendChild(el("span", "trace-ip", ok.Address.IP));
      if (ok.Hostname) tdIp.appendChild(el("span", "trace-host", ok.Hostname));
      tr.appendChild(tdIp);

      const rtts = samples.filter((s) => s.Success).map((s) => s.RTT / 1e6);
      const tdRtt = document.createElement("td");
      if (rtts.length) {
        const avg = rtts.reduce((a, b) => a + b, 0) / rtts.length;
        tdRtt.className = "trace-rtt" + (avg < 50 ? " fast" : avg > 200 ? " slow" : "");
        tdRtt.textContent = rtts.map((r) => r.toFixed(1)).join(" / ") + " ms";
      } else { tdRtt.textContent = "-"; }
      tr.appendChild(tdRtt);

      tr.appendChild(td("trace-loc", formatGeo(ok.Geo)));

      const parts = []; if (ok.Geo && ok.Geo.asnumber) parts.push("AS" + ok.Geo.asnumber);
      if (ok.Geo && ok.Geo.isp) parts.push(ok.Geo.isp); else if (ok.Geo && ok.Geo.owner) parts.push(ok.Geo.owner);
      tr.appendChild(td("trace-asn", parts.join(" · ") || "-"));

      tbody.appendChild(tr);
    }
  }

  function formatGeo(geo) {
    if (!geo) return "-";
    const parts = []; if (geo.country) parts.push(geo.country); if (geo.prov && geo.prov !== geo.country) parts.push(geo.prov); if (geo.city) parts.push(geo.city);
    return parts.join(" ") || "-";
  }

  function escapeHtml(s) { const d = document.createElement("div"); d.textContent = s; return d.innerHTML; }

  function setTraceStatus(html) { $("#traceSummary").innerHTML = html; }

  /* ---------- fast trace ---------- */

  let ftData = [], ftEventSrc = null, ftActive = 0;

  function initFtTabs() {
    const targets = ftData.length ? ftData : [];
    for (let i = 0; i < 3; i++) {
      const tab = document.getElementById("ftTab" + i);
      if (!tab) continue;
      tab.textContent = targets[i] ? targets[i].name : "—";
      let cls = "ft-tab"; if (i === ftActive) cls += " active";
      if (targets[i]) { if (targets[i].error) cls += " error"; else if (targets[i].result) cls += " done"; }
      tab.className = cls;
    }
    if (ftData.length > ftActive && ftData[ftActive] && ftData[ftActive].result) {
      renderJsonTrace(ftData[ftActive].result.json, $("#ftBody"));
    } else if (ftData.length > ftActive && ftData[ftActive] && ftData[ftActive].error) {
      $("#ftBody").innerHTML = '<tr><td colspan="5" class="trace-timeout err">' + i18n("ft_error", escapeHtml(ftData[ftActive].error)) + '</td></tr>';
    } else if (ftData.length > ftActive && ftData[ftActive]) {
      $("#ftBody").innerHTML = '<tr><td colspan="5" class="trace-timeout">' + i18n("ft_tracing", escapeHtml(ftData[ftActive].name)) + '</td></tr>';
    }
  }

  document.getElementById("ftTabs").addEventListener("click", (e) => {
    if (!e.target.classList.contains("ft-tab")) return;
    ftActive = parseInt(e.target.dataset.idx, 10);
    initFtTabs();
  });

  document.getElementById("ftBtn").addEventListener("click", () => {
    ftData = []; ftActive = 0;
    const ftBtn = $("#ftBtn"), ftBody = $("#ftBody"), ftElapsed = $("#ftElapsed");
    ftBtn.disabled = true;
    ftBody.innerHTML = '<tr><td colspan="5" class="trace-timeout">' + i18n("ft_connecting") + '</td></tr>';
    ftElapsed.textContent = "";

    ftEventSrc = new EventSource("/api/fasttrace");

    ftEventSrc.addEventListener("target", (ev) => {
      const meta = JSON.parse(ev.data);
      ftData.push({ name: meta.name, host: meta.host, result: null, elapsed: null, error: null });
      const idx = ftData.length - 1;
      const tab = document.getElementById("ftTab" + idx); if (tab) tab.classList.add("running");
      const cur = ftData[ftActive];
      if (!cur || !cur.result) ftActive = idx;
      initFtTabs();
    });

    ftEventSrc.addEventListener("result", (ev) => {
      const data = JSON.parse(ev.data);
      const idx = ftData.findIndex((d) => d.host === data.host);
      if (idx >= 0) { ftData[idx].result = { json: data.json, elapsed: data.elapsed }; ftData[idx].elapsed = data.elapsed; }
      ftActive = idx >= 0 ? idx : ftData.length - 1;
      initFtTabs();
      const elapsed = ftData.filter((d) => d.elapsed != null).reduce((s, d) => s + d.elapsed, 0);
      ftElapsed.textContent = i18n("ft_elapsed", elapsed.toFixed(0));
    });

    ftEventSrc.addEventListener("error", (ev) => {
      const data = JSON.parse(ev.data);
      const idx = ftData.findIndex((d) => d.host === data.host);
      if (idx >= 0) { ftData[idx].error = data.error; }
      ftActive = idx >= 0 ? idx : ftData.length - 1;
      initFtTabs();
    });

    ftEventSrc.addEventListener("done", () => {
      const elapsed = ftData.filter((d) => d.elapsed != null).reduce((s, d) => s + d.elapsed, 0);
      ftElapsed.textContent = i18n("ft_all_done", elapsed.toFixed(0));
      closeFt();
    });

    ftEventSrc.onerror = () => { ftElapsed.textContent = (ftElapsed.textContent || "") + i18n("ft_lost"); closeFt(); };
  });

  function closeFt() { if (ftEventSrc) { ftEventSrc.close(); ftEventSrc = null; } const b = $("#ftBtn"); if (b) b.disabled = false; }

  /* ---------- unlock ---------- */

  $("#unlockBtn").addEventListener("click", async () => {
    const btn = $("#unlockBtn"), grid = $("#unlockGrid");
    btn.disabled = true;
    grid.innerHTML = `<div style="grid-column:1/-1;text-align:center;padding:30px;color:var(--ink-3)">${i18n("unlock_loading")}</div>`;

    try {
      const resp = await fetch("/api/unlock");
      if (!resp.ok) {
        grid.innerHTML = `<div style="grid-column:1/-1;color:#dc2626;text-align:center;padding:20px">${i18n("err_unlock", escapeHtml(await resp.text()))}</div>`;
        return;
      }
      const data = await resp.json();
      renderUnlock(data, grid);
    } catch (err) {
      grid.innerHTML = `<div style="grid-column:1/-1;color:#dc2626;text-align:center;padding:20px">${i18n("err_connect", escapeHtml(err.message))}</div>`;
    } finally { btn.disabled = false; }
  });

  function renderUnlock(data, grid) {
    grid.innerHTML = "";
    const badgeMap = { YES: "yes", NO: "no", Banned: "banned", ERR: "err", Restricted: "restricted", Failed: "err", Unexpected: "err" };
    const labelMap = { YES: "✓", NO: "✗", Banned: "⊘", ERR: "!", Restricted: "◐", Failed: "!", Unexpected: "?" };

    for (const cat of (data.categories || [])) {
      const catDiv = document.createElement("div");
      catDiv.className = "unlock-cat";
      catDiv.textContent = cat.category + " · " + i18n("unlock_items", cat.services.length);
      grid.appendChild(catDiv);

      for (const svc of (cat.services || [])) {
        const card = document.createElement("div"); card.className = "unlock-card";
        card.appendChild(el("div", "unlock-badge badge-" + (badgeMap[svc.result] || "no"), labelMap[svc.result] || "?"));
        card.appendChild(el("div", "unlock-name", svc.name));
        card.appendChild(el("div", "unlock-region", svc.region || svc.result));
        grid.appendChild(card);
      }
    }

    const footer = document.createElement("div");
    footer.style.cssText = "grid-column:1/-1;font-size:12px;color:var(--ink-3);margin-top:16px;text-align:center";
    footer.textContent = i18n("unlock_footer", data.ipv4 || "-", data.isp || "-");
    grid.appendChild(footer);
  }

  /* ---------- terminal helpers ---------- */

  function startTerminal(title) {
    terminal.textContent = "";
    terminalTitle.textContent = title;
    terminalWrap.classList.remove("hidden");
    stopBtn.classList.remove("hidden");
    terminal.scrollTop = 0;
  }
  function finishTerminal() { stopBtn.classList.add("hidden"); abortCtl = null; }

  function stopRunning() {
    if (abortCtl) abortCtl.abort();
    if (ftEventSrc) { ftEventSrc.close(); ftEventSrc = null; const b = $("#ftBtn"); if (b) b.disabled = false; }
    if (speedCtl) { speedCtl.abort(); speedCtl = null; speedBtn.disabled = false; }
    runBtn.disabled = false;
    finishTerminal();
  }

  stopBtn.addEventListener("click", stopRunning);

  function appendLine(text, cls) {
    terminal.appendChild(el("span", cls || "", text + "\n"));
    terminal.scrollTop = terminal.scrollHeight;
  }

  /* ==================== speedtest ==================== */

  const speedBtn = $("#speedBtn"), gauge = $("#gauge"), gaugeArc = $("#gaugeArc");
  const gaugeValue = $("#gaugeValue"), gaugePhase = $("#gaugePhase"), speedNote = $("#speedNote");
  const statDown = $("#statDown"), statUp = $("#statUp"), statPing = $("#statPing"), statJitter = $("#statJitter");

  const ARC_LEN = 471.24, CIRC = 628.32, MAX_GAUGE = 1000, DOWNLOAD_DURATION = 10, UPLOAD_DURATION = 10, PARALLEL_STREAMS = 4;
  let speedCtl = null, speedToken = null;

  speedBtn.addEventListener("click", runSpeedtest);

  async function runSpeedtest() {
    if (speedCtl) return;
    speedBtn.disabled = true;
    speedNote.classList.remove("error");
    resetGauge();

    try {
      setPhase(i18n("sp_acquire"));
      speedToken = await acquirePermit();
      speedCtl = new AbortController();

      setPhase(i18n("sp_latency"));
      const { ping, jitter } = await measureLatency(speedCtl.signal);
      statPing.textContent = ping.toFixed(0);
      statJitter.textContent = jitter.toFixed(0);

      setPhase(i18n("sp_download"));
      gauge.classList.add("running"); gauge.classList.remove("upload");
      const down = await measureDownload(speedCtl.signal, (mbps) => { setGauge(mbps); statDown.textContent = mbps.toFixed(1); });
      statDown.textContent = down.toFixed(1);
      setGauge(down);

      setPhase(i18n("sp_upload"));
      gauge.classList.remove("running"); gauge.classList.add("upload");
      const up = await measureUpload(speedCtl.signal, (mbps) => { setGauge(mbps); statUp.textContent = mbps.toFixed(1); });
      statUp.textContent = up.toFixed(1);

      gauge.classList.remove("upload"); gauge.classList.add("done");
      setPhase(i18n("sp_done"));
      setGauge(Math.max(down, up));
      const t = new Date().toLocaleTimeString("zh-TW");
      speedNote.textContent = i18n("sp_done_msg", t, down.toFixed(1), up.toFixed(1), ping.toFixed(0));
    } catch (err) {
      if (err.name === "AbortError") speedNote.textContent = i18n("sp_cancelled");
      else { speedNote.textContent = i18n("sp_failed", err.message); speedNote.classList.add("error"); }
      setPhase(i18n("sp_ready"));
      gauge.classList.remove("running", "upload", "done");
    } finally { speedCtl = null; speedBtn.disabled = false; }
  }

  function resetGauge() {
    gauge.classList.remove("running", "upload", "done");
    gaugeArc.style.strokeDasharray = `0 ${CIRC}`;
    gaugeValue.textContent = "--";
    statDown.textContent = statUp.textContent = statPing.textContent = statJitter.textContent = "--";
    speedNote.textContent = i18n("sp_running");
  }

  function setPhase(text) { gaugePhase.textContent = text; }

  async function acquirePermit() {
    const resp = await fetch("/api/speedtest/begin", { method: "POST" });
    if (resp.status === 429) {
      const body = await resp.json().catch(() => ({}));
      throw new Error(i18n("sp_rate_limit", body.retry_after || i18n("sp_rate_limit_later")));
    }
    if (!resp.ok) throw new Error(i18n("sp_permit_fail", resp.status));
    return (await resp.json()).token;
  }

  function setGauge(mbps) {
    const clamped = Math.min(mbps, MAX_GAUGE), frac = clamped / MAX_GAUGE;
    gaugeArc.style.strokeDasharray = `${(frac * ARC_LEN).toFixed(1)} ${CIRC}`;
    gaugeValue.textContent = mbps >= 100 ? mbps.toFixed(0) : mbps.toFixed(1);
  }

  async function measureLatency(signal) {
    const samples = [];
    for (let i = 0; i < 10; i++) {
      if (signal.aborted) throw new DOMException("aborted", "AbortError");
      const t0 = performance.now();
      await fetch("/api/ping?_=" + i + Date.now(), { cache: "no-store", signal, headers: { "X-Speedtest-Token": speedToken } });
      samples.push(performance.now() - t0);
      await sleep(80);
    }
    samples.sort((a, b) => a - b);
    const ping = samples[Math.floor(samples.length / 2)];
    const mean = samples.reduce((s, v) => s + v, 0) / samples.length;
    const jitter = Math.sqrt(samples.reduce((s, v) => s + (v - mean) ** 2, 0) / samples.length);
    return { ping, jitter };
  }

  async function measureDownload(signal, onUpdate) {
    let totalBytes = 0; const start = performance.now();
    const workers = [];
    for (let i = 0; i < PARALLEL_STREAMS; i++) workers.push(downloadWorker(i, signal, (n) => { totalBytes += n; }));
    const ticker = setInterval(() => { const el = (performance.now() - start) / 1000; if (el > 0) onUpdate((totalBytes * 8) / el / 1e6); }, 200);
    await sleep(DOWNLOAD_DURATION * 1000); clearInterval(ticker);
    workers.forEach((w) => w.cancel());
    await Promise.allSettled(workers.map((w) => w.promise));
    return (totalBytes * 8) / ((performance.now() - start) / 1000) / 1e6;
  }

  function downloadWorker(id, signal, onBytes) {
    let cancelled = false;
    const promise = (async () => {
      for (;;) {
        if (cancelled || signal.aborted) return;
        try {
          const resp = await fetch(`/download/25mb?_w=${id}&_t=${Date.now()}`, { cache: "no-store", signal, headers: { "X-Speedtest-Token": speedToken } });
          if (!resp.ok || !resp.body) return;
          const reader = resp.body.getReader();
          for (;;) { const { done, value } = await reader.read(); if (done) break; if (cancelled || signal.aborted) { reader.cancel(); return; } onBytes(value.byteLength); }
        } catch (err) { if (err.name === "AbortError") return; await sleep(300); if (cancelled || signal.aborted) return; }
      }
    })();
    return { promise, cancel: () => { cancelled = true; } };
  }

  async function measureUpload(signal, onUpdate) {
    const CHUNK_SIZE = 2 * 1024 * 1024;
    const chunk = new Uint8Array(CHUNK_SIZE);
    for (let off = 0; off < CHUNK_SIZE; off += 65536) crypto.getRandomValues(chunk.subarray(off, Math.min(off + 65536, CHUNK_SIZE)));
    const blob = new Blob([chunk], { type: "application/octet-stream" });
    let totalBytes = 0; const start = performance.now();
    const ticker = setInterval(() => { const el = (performance.now() - start) / 1000; if (el > 0) onUpdate((totalBytes * 8) / el / 1e6); }, 200);
    const workers = [];
    for (let i = 0; i < PARALLEL_STREAMS; i++) workers.push(uploadWorker(blob, signal, (n) => { totalBytes += n; }));
    await sleep(UPLOAD_DURATION * 1000); clearInterval(ticker);
    workers.forEach((w) => w.cancel());
    await Promise.allSettled(workers.map((w) => w.promise));
    return (totalBytes * 8) / ((performance.now() - start) / 1000) / 1e6;
  }

  function uploadWorker(blob, signal, onBytes) {
    let cancelled = false;
    const promise = (async () => {
      for (;;) {
        if (cancelled || signal.aborted) return;
        try {
          const resp = await fetch("/api/upload", { method: "POST", body: blob, headers: { "Content-Type": "application/octet-stream", "X-Speedtest-Token": speedToken }, cache: "no-store", signal });
          if (resp.ok) onBytes(blob.size); else await resp.arrayBuffer();
        } catch (err) { if (err.name === "AbortError") return; await sleep(200); if (cancelled || signal.aborted) return; }
      }
    })();
    return { promise, cancel: () => { cancelled = true; } };
  }

  function sleep(ms) { return new Promise((r) => setTimeout(r, ms)); }

  /* ---------- initial route activation ----------
   * Runs once after the IIFE has wired up all event listeners. The server
   * tells us which tool the user landed on via <body data-initial-tool>; if
   * absent we honour the URL pathname (sidebar-driven state would not be
   * present yet on a fresh load). */
  (function initFromURL() {
    const fromBody = (document.body && document.body.dataset.initialTool) || "";
    const initial = (fromBody && VALID_SLUGS.has(fromBody)) ? fromBody : (currentSlugFromURL() || "");
    if (initial && initial !== currentTool) {
      activateTool(initial, { push: false });
    }
    // Prefill the diagnostic target input from ?target= for direct links
    // like /tools/ping?target=8.8.8.8. Other tools (speedtest/fasttrace/
    // unlock) ignore the input entirely, so this is harmless if they ever
    // receive such a query.
    const target = new URLSearchParams(location.search).get("target");
    if (target) targetInput.value = target;
  })();
})();
