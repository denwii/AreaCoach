/* Area Coach — user interface */
(function () {
  "use strict";
  const token = new URLSearchParams(location.search).get("t") || "";
  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));
  const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  const LOCALE = "en-US";
  const NF = {};
  const nf = (v, d = 1) => {
    if (v == null || !isFinite(v)) return "–";
    const key = d;
    NF[key] = NF[key] || new Intl.NumberFormat(LOCALE, { minimumFractionDigits: d, maximumFractionDigits: d });
    return NF[key].format(v);
  };
  const nfTrim = (v, d = 2) => {
    if (v == null || !isFinite(v)) return "–";
    return new Intl.NumberFormat(LOCALE, { maximumFractionDigits: d, useGrouping: false }).format(v);
  };
  const pct = (v, d = 0) => (v == null || !isFinite(v) ? "–" : nf(v * 100, d) + "%");
  const signed = (v, d = 1) => (v > 0 ? "+" : v < 0 ? "−" : "") + nf(Math.abs(v), d);
  const dateFmt = new Intl.DateTimeFormat("en-GB", { day: "numeric", month: "short" });
  const dateTime = new Intl.DateTimeFormat("en-GB", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
  const d = (s) => (s ? new Date(s) : null);
  // a movement on the tablet (mm, tablet axes) as seen on the screen: the
  // area may be rotated in OpenTabletDriver, by any angle
  function userVec(dx, dy, rot) {
    const t = ((rot || 0) * Math.PI) / 180, c = Math.cos(t), s = Math.sin(t);
    return [c * dx + s * dy, -s * dx + c * dy];
  }
  const CAT_COLORS = ["var(--c1)", "var(--c2)", "var(--c3)", "var(--c4)", "var(--c5)"];
  const CS_COLORS = ["var(--c4)", "var(--c1)", "var(--c3)", "var(--c2)"];

  const ICON = {
    good: '<svg viewBox="0 0 16 16" class="tone-good"><circle cx="8" cy="8" r="6.5" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="M5 8.2l2 2 4-4.4" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>',
    warn: '<svg viewBox="0 0 16 16" class="tone-warn"><path d="M8 2l6.5 11.5h-13z" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/><path d="M8 6.5v3.2M8 11.6v.2" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/></svg>',
    neutral: '<svg viewBox="0 0 16 16" class="tone-neutral"><circle cx="8" cy="8" r="6.5" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="M8 7v4M8 4.8v.2" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/></svg>',
    error: '<svg viewBox="0 0 16 16" style="color:var(--bad)"><circle cx="8" cy="8" r="6.5" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="M5.6 5.6l4.8 4.8M10.4 5.6l-4.8 4.8" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/></svg>',
    info: '<svg viewBox="0 0 16 16" class="tone-neutral"><circle cx="8" cy="8" r="6.5" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="M8 7v4M8 4.8v.2" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/></svg>',
    copy: '<svg viewBox="0 0 24 24"><rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V5a1 1 0 0 0-1-1H5a1 1 0 0 0-1 1v10a1 1 0 0 0 1 1h3"/></svg>',
    folder: '<svg viewBox="0 0 24 24"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>',
    play: '<svg viewBox="0 0 24 24"><path d="M7 5l12 7-12 7z"/></svg>',
  };

  const state = {
    status: null,
    report: null,
    version: -1,
    busy: false,
    route: "area",
    areaShow: { now: true, free: false, shift: false, final: true, past: false },
    areaSel: "final",
    areaZoom: true,
    plays: { rows: [], total: 0, q: "", offset: 0 },
    tablets: null,
  };

  /* ---------------- api ---------------- */
  async function api(path, body) {
    const opts = { headers: { "X-Token": token } };
    if (body !== undefined) {
      opts.method = "POST";
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(body);
    }
    const r = await fetch("/api/" + path, opts);
    const j = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(j.error || "Error " + r.status);
    return j;
  }

  function toast(msg) {
    const t = $("#toast");
    t.textContent = msg;
    t.classList.add("on");
    clearTimeout(toast._t);
    toast._t = setTimeout(() => t.classList.remove("on"), 2600);
  }

  async function copyText(txt) {
    try {
      await navigator.clipboard.writeText(txt);
    } catch {
      const ta = document.createElement("textarea");
      ta.value = txt;
      document.body.appendChild(ta);
      ta.select();
      document.execCommand("copy");
      ta.remove();
    }
    toast("Copied: " + txt);
  }

  /* ---------------- data refresh ---------------- */
  async function refreshStatus() {
    try {
      state.status = await api("status");
      applyTheme();
      renderStatus();
      if (state.status.reportVersion !== state.version && !state.status.reportBusy) refreshReport();
    } catch (e) {
      $("#status").innerHTML = `<span class="pill"><span class="dot"></span>Cannot reach the program</span>`;
    }
  }
  async function refreshReport() {
    try {
      const r = await api("report");
      state.busy = r.busy;
      if (r.report && r.version !== state.version) {
        state.report = r.report;
        state.version = r.version;
        render();
      } else if (!state.report) {
        render();
      }
      renderStatus();
    } catch (e) {
      /* ignore */
    }
  }

  function applyTheme() {
    const t = state.status?.settings?.theme || "";
    if (t === "dark" || t === "light") document.documentElement.dataset.theme = t;
    else delete document.documentElement.dataset.theme;
  }

  const profName = (id) => ((state.status?.profiles || []).find((p) => p.id === id) || {}).name || "–";
  function renderProfiles() {
    const ps = state.status?.profiles || [];
    const el = $("#profileSel");
    const key = JSON.stringify(ps.map((p) => [p.id, p.name, p.active]));
    if (!el || el.dataset.key === key) return;
    el.dataset.key = key;
    if (!ps.length) { el.innerHTML = ""; return; }
    el.innerHTML = `<label for="profSelect">Grip</label><select id="profSelect" aria-label="Grip profile">${ps.map((p) => `<option value="${p.id}" ${p.active ? "selected" : ""}>${esc(p.name)}</option>`).join("")}<option value="new">New profile…</option></select>`;
    $("#profSelect").addEventListener("change", async (ev) => {
      const v = ev.target.value;
      const cur = ps.find((p) => p.active);
      if (v === "new") {
        ev.target.value = cur ? String(cur.id) : "";
        location.hash = "#/settings";
        setTimeout(() => { const i = $("#newProfile"); if (i) { i.scrollIntoView({ block: "center" }); i.focus(); } }, 120);
        return;
      }
      await switchProfile(+v);
    });
  }
  async function switchProfile(id) {
    try {
      await api("profile", { action: "switch", id });
      toast(`Profile "${profName(id)}": only its replays are used`);
      state.version = -1;
      await refreshStatus();
      if (state.route === "plays") await loadPlays(true);
      render();
    } catch (e) { toast(e.message); }
  }

  function renderStatus() {
    const s = state.status;
    if (!s) return;
    renderProfiles();
    const parts = [];
    const imp = s.import || {};
    if (imp.running) {
      parts.push(`<span class="pill"><span class="dot busy"></span>Importing replays ${imp.done}/${imp.total}</span>`);
    } else if (state.busy || s.reportBusy) {
      parts.push(`<span class="pill"><span class="dot busy"></span>Analysing</span>`);
    }
    if (s.calib && s.calib.mode === "adapt" && s.calib.adapt && s.calib.adapt.active) parts.push(`<span class="pill"><span class="dot on"></span>Diagnosis running</span>`);
    if (state.report?.generatedAt) parts.push(`<span>Updated ${dateTime.format(d(state.report.generatedAt))}</span>`);
    $("#status").innerHTML = parts.join("");
  }

  /* ---------------- helpers ---------------- */
  const rectVals = (r) => [
    ["Width", r.w, "mm"],
    ["Height", r.h, "mm"],
    ["X", r.x, "mm"],
    ["Y", r.y, "mm"],
    ["Rotation", r.rot || 0, "°"],
  ];
  const otdText = (r) => `Width ${nfTrim(r.w)} · Height ${nfTrim(r.h)} · X ${nfTrim(r.x)} · Y ${nfTrim(r.y)} · Rotation ${nfTrim(r.rot || 0)}°`;

  function otdTable(cur, next) {
    const target = next || cur;
    const cells = rectVals(target).map(([k, v, u], i) => {
      const old = rectVals(cur)[i][1];
      const ch = next && Math.abs(v - old) >= 0.005;
      const delta = ch ? `${signed(v - old, 2)} ${u}` : next ? "same" : "";
      return `<div class="otd-cell ${ch ? "changed" : ""}"><div class="k">${k}</div><div class="v readout">${nfTrim(v)}<span class="unit">${u}</span></div><div class="d">${delta}</div><button class="copy" data-copy="${nfTrim(v)}" aria-label="Copy ${k}">copy</button></div>`;
    });
    return `<div class="otd"><div class="otd-head"><span>${next ? "Values to set in OpenTabletDriver" : "Area in OpenTabletDriver now"}</span><button class="btn small ghost" data-copy="${esc(otdText(target))}">${ICON.copy}Copy all</button></div><div class="otd-grid">${cells.join("")}</div>${next ? `<div class="otd-foot">After typing them in, press <b>Save</b> in OpenTabletDriver, not just Apply: the area is read from the saved file and the change is recorded by itself.</div>` : ""}</div>`;
  }

  function bindCopy(root) {
    $$("[data-copy]", root).forEach((b) => b.addEventListener("click", () => copyText(b.dataset.copy)));
  }

  function warnList(ws) {
    if (!ws || !ws.length) return "";
    return `<div class="warns">${ws.map((w) => `<div class="warn-item ${w.level}">${ICON[w.level] || ICON.info}<span>${esc(w.text)}</span></div>`).join("")}</div>`;
  }

  function sectionHead(title, desc, extra = "") {
    return `<header><div><h2 class="h-sec">${esc(title)}</h2>${desc ? `<p>${desc}</p>` : ""}</div>${extra}</header>`;
  }

  function mapName(p) {
    if (p.artist && p.title) return `${p.artist} – ${p.title}`;
    return p.title || p.artist || "Untitled map";
  }

  function needsData(title, text) {
    return `<div class="empty"><h2>${esc(title)}</h2><p>${text}</p></div>`;
  }

  /* ---------------- pages ---------------- */
  const PAGES = {
    diagnosis: { title: "Diagnosis", render: pageTest },
    area: { title: "Area", render: pageHome },
    progress: { title: "Progress", render: pageProgress },
    plays: { title: "Plays", render: pagePlays },
    filter: { title: "Filter", render: pageFilter },
    settings: { title: "Settings", render: pageSettings },
    // details, reached from the Area page
    areas: { title: "Area · every area from the replays", render: pageArea, rail: "area" },
    aim: { title: "Area · aim", render: pageAim, rail: "area" },
    consistency: { title: "Area · error by size", render: pageConsistency, rail: "area" },
  };
  // where to start: the diagnosis until there is a result
  function defaultRoute() {
    const s = state.status;
    const c = s && s.calib;
    if (c && c.exists && c.active) return "diagnosis";
    if (s && s.calibResult) return "area";
    return "diagnosis";
  }
  const currentRoute = () => (location.hash.replace(/^#\/?/, "") || defaultRoute()).split("?")[0];

  function render() {
    clearInterval(state.liveTimer);
    clearInterval(state.livePoll);
    const route = currentRoute();
    state.route = PAGES[route] ? route : "area";
    const railKey = PAGES[state.route].rail || state.route;
    $$(".rail a").forEach((a) => a.classList.toggle("active", a.dataset.route === railKey));
    const p = PAGES[state.route];
    $("#pageTitle").textContent = p.title;
    Charts.hideTip();
    const view = $("#view");
    const scroll = view.scrollTop;
    const same = view.dataset.route === state.route;
    view.dataset.route = state.route;
    p.render(view);
    if (same) view.scrollTop = scroll;
    bindCopy(view);
  }

  /* ----- Area (home) ----- */
  // the one recommended area: the last diagnosis, or the replays until there is one
  function idealInfo() {
    const s = state.status, r = state.report;
    const cr = s && s.calibResult;
    const a = r && r.areas;
    if (cr && cr.rect) return { rect: cr.rect, source: "diag", cr };
    if (a && a.final) return { rect: a.final, source: "replay" };
    return null;
  }
  const closeRect = (x, y) => x && y && Math.abs(x.w - y.w) < 0.6 && Math.abs(x.h - y.h) < 0.6 && Math.hypot(x.x - y.x, x.y - y.y) < 1 && Math.abs((x.rot || 0) - (y.rot || 0)) < 0.5;

  function pageHome(view) {
    const s = state.status;
    if (!s) { view.innerHTML = ""; return; }
    const r = state.report;
    const a = r && r.areas;
    const env = s.env || {};
    const cur = (a && a.current) || (env.profile && env.profile.area);
    const ideal = idealInfo();
    const c = s.calib || {};
    const t = r && r.totals;
    if (!env.setupOk && (!r || r.coach?.phase === "setup")) {
      view.innerHTML = `<div class="page"><div class="empty"><h2>${esc(r?.coach?.title || "Set up OpenTabletDriver and osu!")}</h2><p>${esc(r?.coach?.message || "I can't find the OpenTabletDriver settings or the osu! folder.")}</p><p><a class="btn primary" href="#/settings">Open settings</a></p></div>${warnList(env.warnings)}</div>`;
      return;
    }
    let head, body = "";
    const done = !!(s.calibResult && s.calibResult.rect);
    const diagLink = `<a class="btn ${ideal ? "ghost" : "primary"}" href="#/diagnosis">${c.exists && c.mode === "adapt" ? "Open the diagnosis" : done ? "Run the diagnosis again" : "Run the diagnosis"}</a>`;
    if (!ideal) {
      head = `<h1 class="verdict">Let's find your area</h1><p class="verdict-msg">Run the in-game diagnosis: you play your own maps and the area shapes itself around you. A few maps are enough to get close; it keeps refining as long as you play.</p>`;
      body = `<div class="row" style="margin-top:18px">${diagLink}</div>`;
    } else {
      const same = closeRect(cur, ideal.rect);
      const src = ideal.source === "diag"
        ? `From the in-game diagnosis of ${dateFmt.format(d(ideal.cr.at))} · ${ideal.cr.mode === "live" || ideal.cr.mode === "adapt" ? `${ideal.cr.samples} stretches measured` : `${ideal.cr.samples} test maps`}${ideal.cr.precise ? "" : " · still settling"}${c.exists && c.mode === "adapt" ? " · keeps refining while you play with the diagnosis" : ""}`
        : `Worked out from your ${nf(t ? t.plays : 0, 0)} replays. The in-game diagnosis finds it in a few minutes, and more precisely`;
      if (same) {
        head = `<h1 class="verdict">Your area is the right one</h1><p class="verdict-msg">${esc(src)}.</p>`;
      } else {
        head = `<h1 class="verdict">${ideal.source === "diag" ? "Ideal area" : "Suggested area"}: ${nfTrim(ideal.rect.w, 1)} × ${nfTrim(ideal.rect.h, 1)} mm</h1><p class="verdict-msg">${esc(src)}.</p>`;
        body = `${otdTable(cur || ideal.rect, ideal.rect)}<div class="row" style="margin-top:12px;flex-wrap:wrap"><button class="btn primary" id="applyIdeal">Set in OpenTabletDriver</button>${diagLink}</div>`;
        if (c.exists && c.mode === "adapt" && c.active) body = `<p class="small" style="margin-top:12px">The diagnosis is running: it keeps the area in OpenTabletDriver up to date by itself at the end of every map.</p><div class="row" style="margin-top:12px"><a class="btn primary" href="#/diagnosis">Open the diagnosis</a></div>`;
      }
      if (same) body = `<div class="row" style="margin-top:14px">${diagLink}</div>`;
      if (ideal.source === "diag" && ideal.cr.hi > ideal.cr.lo && ideal.cr.hi - ideal.cr.lo >= 2) {
        body += `<p class="small muted" style="margin-top:12px">Anywhere between ${nfTrim(ideal.cr.lo, 0)} and ${nfTrim(ideal.cr.hi, 0)} mm works almost the same: if a size in that range feels better, that's fine too.</p>`;
      }
      // the filter that goes with it
      // the guided filter, once it has measured maps; else the simulation
      const rf = ideal.source === "diag" && ideal.cr.filter;
      const fcb = c.adapt && c.adapt.coach;
      if (ideal.source === "diag" && fcb && fcb.nOn > 0) {
        body += fcb.bestOn
          ? `<p class="small" style="margin-top:12px"><b>Filter to use with this area:</b> AbstractQbit's Radial Follow, <span class="readout">${rfShort(fcb.best)}</span> · from the guided filter, ${nf(fcb.pct || 0, 1)}% perfection · <a href="#/diagnosis">details</a></p>`
          : `<p class="small" style="margin-top:12px"><b>Filter:</b> the guided filter found that you do better without Radial Follow.</p>`;
      } else if (rf) {
        body += rf.best === "none"
          ? `<p class="small" style="margin-top:12px"><b>Filter:</b> simulated on your movements, Radial Follow doesn't help you with this area.</p>`
          : `<p class="small" style="margin-top:12px"><b>Filter to try with this area:</b> AbstractQbit's Radial Follow, <span class="readout">${rfShort(rf.params)}</span> (simulated on your movements; the guided filter in the diagnosis tunes it on how you play) · <a href="#/diagnosis">details</a></p>`;
      }
      // the replays keep checking the result
      if (ideal.source === "diag" && a && a.final && t && t.plays >= 15 && Math.abs(a.final.w - ideal.rect.w) > Math.max(4, 0.06 * ideal.rect.w)) {
        body += `<p class="small muted" style="margin-top:6px">Over the long run your replays point to ${nfTrim(a.final.w, 1)} mm instead. If the area keeps feeling uncomfortable, run the diagnosis again.</p>`;
      }
    }
    const busy = c.exists && c.active ? `<div class="callout" style="margin-bottom:22px"><div><b>Diagnosis running</b><p>${(c.adapt && c.adapt.nMaps) || 0} maps measured: keep playing, at the end of each map I set the area for the next one.</p></div><div class="row"><a class="btn primary" href="#/diagnosis">Open the diagnosis</a></div></div>` : "";
    const ev = (r && r.coach && r.coach.evidence) || [];
    const hist = ((r && r.coach && r.coach.history) || []).slice().reverse().slice(0, 10);
    view.innerHTML = `<div class="page">
      ${busy}
      <div class="hero">
        <div class="stage"><div class="chart" id="homeTablet"></div>
          <div class="legend"><span class="swatch now"><i></i>Your area</span>${ideal && !closeRect(cur, ideal.rect) ? `<span class="swatch next"><i></i>Ideal area</span>` : ""}${a && a.playfield ? `<span class="swatch pf"><i></i>Playfield</span>` : ""}</div></div>
        <div>${head}${body}</div>
      </div>
      ${warnList((env.warnings || []).filter((w) => w.level !== "info"))}
      ${t ? `<div class="stats">
        <div class="stat"><div class="k">Notes analysed</div><div class="v readout">${nf(t.aimHits, 0)}</div><div class="h">${nf(t.plays, 0)} plays</div></div>
        <div class="stat"><div class="k">With the current area</div><div class="v readout">${nf(t.epochHits, 0)}</div><div class="h">${nf(t.epochPlays, 0)} plays</div></div>
        <div class="stat"><div class="k">Average error</div><div class="v readout">${pct(t.normErr)}</div><div class="h">of the circle radius</div></div>
        <div class="stat"><div class="k">Aim misses</div><div class="v readout">${nf(t.missPer1k, 1)}</div><div class="h">per 1000 notes</div></div>
      </div>` : ""}
      <section class="section">
        ${ev.length ? `<details class="why"><summary>What the replays say</summary><ul class="evidence">${ev.map((e) => `<li>${ICON[e.tone] || ICON.neutral}<span>${esc(e.text)}</span></li>`).join("")}</ul><p style="margin-top:10px" class="small"><a href="#/areas">Every area worked out from the replays</a> · <a href="#/aim">Aim in detail</a> · <a href="#/consistency">Error by size</a></p></details>` : ""}
        ${hist.length ? `<details class="why"><summary>History</summary><ul class="history">${hist.map((h) => `<li><span class="when">${dateFmt.format(d(h.date))}</span><span class="mark ${h.kind}"></span><span>${esc(h.text)}</span></li>`).join("")}</ul></details>` : ""}
      </section>
    </div>`;
    const ap = $("#applyIdeal");
    if (ap) ap.addEventListener("click", async () => {
      ap.disabled = true;
      try { await api("apply", { area: ideal.rect }); toast("Area set in OpenTabletDriver"); await refreshStatus(); render(); } catch (e) { toast(e.message); ap.disabled = false; }
    });
    if (cur && a) {
      const areas = [{ rect: cur, kind: "now", label: "yours" }];
      if (ideal && !closeRect(cur, ideal.rect)) areas.push({ rect: ideal.rect, kind: "next", label: "ideal" });
      Charts.draw($("#homeTablet"), Charts.tablet, { tw: a.tabletW, th: a.tabletH, tabletName: a.tabletName, areas, playfield: a.playfield, maxHeight: 440 });
    } else if (cur && env.setup) {
      const areas = [{ rect: cur, kind: "now", label: "yours" }];
      if (ideal && !closeRect(cur, ideal.rect)) areas.push({ rect: ideal.rect, kind: "next", label: "ideal" });
      Charts.draw($("#homeTablet"), Charts.tablet, { tw: env.setup.tabletW, th: env.setup.tabletH, areas, maxHeight: 440 });
    }
  }

  /* ----- Every area from the replays ----- */
  // one line on where the recommendation stands
  function recStatus(r) {
    const rec = r.areas && r.areas.rec;
    if (!rec) return "";
    if (rec.testing) {
      const left = [];
      if (rec.needPlays > rec.testPlays) left.push(rec.needPlays - rec.testPlays === 1 ? "1 more map" : `${rec.needPlays - rec.testPlays} more maps`);
      if (rec.needHits > rec.testHits) left.push(`about ${nf(Math.ceil((rec.needHits - rec.testHits) / 50) * 50, 0)} notes`);
      return `On trial: play ${left.join(" and ")} with this area. Until then the suggestion stays put.`;
    }
    if (rec.kept) return "Same suggestion as last time: the new calculation is within the margin of uncertainty, so it doesn't change on a fluke.";
    return "";
  }
  function pageArea(view) {
    const r = state.report;
    if (!r || !r.areas || !r.areas.current) {
      view.innerHTML = `<div class="page">${needsData("No area", "Set up OpenTabletDriver in the settings.")}</div>`;
      return;
    }
    const a = r.areas;
    const rec = a.rec;
    if (!a.final) {
      view.innerHTML = `<div class="page"><h1 class="h-page">Your areas</h1>${needsData("More data needed", "Play a few maps: the suggestion appears after about 1500 notes hit, that is 3–5 maps.")}</div>`;
      return;
    }
    const rot = (x) => (x.rot ? ` · ${signed(x.rot, 1)}°` : "");
    const defs = [
      { key: "final", kind: "comp", name: "Suggested area", desc: "The width with which you miss least on every kind of pattern, worked out from all replays and all areas. Same proportions as the current area.", rect: a.final, color: "var(--next)" },
      { key: "free", kind: "ratio", name: "Suggested, free proportions", desc: rec && rec.freeRot ? "Same width; height and rotation follow the natural movement of your hand." : "Same width; the height follows the natural movement of your hand.", rect: a.free, color: "var(--alt)" },
      { key: "shift", kind: "shift", name: "Same area, only moved", desc: rec && rec.centered ? "Your current area is already in the right place." : "Current size, moved to where your hand works.", rect: a.shifted, color: "var(--c4)" },
      { key: "now", kind: "now", name: "Current area", desc: "Read from OpenTabletDriver.", rect: a.current, color: "var(--now)" },
    ];
    const sel = defs.find((x) => x.key === state.areaSel && x.rect) || defs[0];
    const choices = defs.filter((x) => x.rect).map((x) => {
      const on = state.areaShow[x.key];
      const err = x.key !== "now" && x.key !== "shift" && x.rect.errW ? `<small>± ${nf(Math.max(x.rect.errW, 0.01), 1)} mm</small>` : "<small>&nbsp;</small>";
      return `<button class="choice ${on ? "on" : ""}" data-key="${x.key}" style="--c:${x.color}" aria-pressed="${on}"><span class="key"></span><span><span class="t">${esc(x.name)}</span><br><span class="d">${esc(x.desc)}</span></span><span class="vals readout">${nfTrim(x.rect.w)} × ${nfTrim(x.rect.h)}${esc(rot(x.rect))}${err}</span></button>`;
    }).join("");
    const epochs = (a.epochs || []).slice().reverse();
    const st = recStatus(r);
    view.innerHTML = `<div class="page">
      <h1 class="h-page">Your areas</h1>
      <p class="lede">The suggestion depends only on your replays: changing the area in OpenTabletDriver doesn't change it, only new plays can. Pick the areas to show on the tablet.</p>
      ${st ? `<div class="note-bar">${ICON.info}<span>${esc(st)}</span></div>` : ""}
      <div class="split" style="margin-top:24px">
        <div><div class="row" style="justify-content:flex-end;margin-bottom:6px"><div class="seg" role="group" aria-label="View"><button data-zoom="1" class="${state.areaZoom ? "on" : ""}">Zoomed in</button><button data-zoom="0" class="${state.areaZoom ? "" : "on"}">Whole tablet</button></div></div><div class="chart" id="areaTablet"></div>
          ${recNote(r)}
        </div>
        <div class="choices">${choices}</div>
      </div>
      ${sel ? `<section class="section">${sectionHead(sel.name, sel.key === "now" ? "" : "Values ready to type into OpenTabletDriver.")}${otdTable(a.current, sel.key === "now" ? null : sel.rect)}
        ${sel.rect.fits === false ? `<div class="warns"><div class="warn-item">${ICON.warn}<span>This area goes past the edges of the tablet: OpenTabletDriver won't accept it as it is.</span></div></div>` : ""}</section>` : ""}
      <section class="section">${sectionHead("Areas used", "One row per area: if you go back to an area you already used, the plays add up. The program records every change made in OpenTabletDriver by itself; the full history is in the settings.")}
        <div class="table-scroll"><table class="data"><thead><tr><th>First used</th><th>Last used</th><th class="num">Width</th><th class="num">Height</th><th class="num">X</th><th class="num">Y</th><th class="num">Rotation</th><th class="num">Notes hit</th></tr></thead>
        <tbody>${epochs.map((e) => `<tr><td>${dateFmt.format(d(e.from))}</td><td>${e.to ? dateFmt.format(d(e.to)) : '<span class="tag cur">in use</span>'}</td><td class="num">${nfTrim(e.area.w)}</td><td class="num">${nfTrim(e.area.h)}</td><td class="num">${nfTrim(e.area.x)}</td><td class="num">${nfTrim(e.area.y)}</td><td class="num">${nfTrim(e.area.rot || 0)}°</td><td class="num">${nf(e.hits, 0)}</td></tr>`).join("")}</tbody></table></div>
      </section>
    </div>`;
    const areas = [];
    if (state.areaShow.past) for (const e of a.epochs || []) if (!e.current) areas.push({ rect: e.area, kind: "past" });
    for (const x of defs.slice().reverse()) if (x.rect && state.areaShow[x.key]) areas.push({ rect: x.rect, kind: x.kind, label: x.key === "now" ? "current" : x.key === "final" ? "suggested" : "", center: x.key !== "now" });
    Charts.draw($("#areaTablet"), Charts.tablet, { tw: a.tabletW, th: a.tabletH, tabletName: a.tabletName, areas, playfield: a.playfield, maxHeight: 520, zoom: state.areaZoom });
    $$("[data-zoom]", view).forEach((b) => b.addEventListener("click", () => { state.areaZoom = b.dataset.zoom === "1"; render(); }));
    $$(".choice", view).forEach((b) => b.addEventListener("click", () => {
      const k = b.dataset.key;
      if (state.areaSel !== k) { state.areaSel = k; state.areaShow[k] = true; }
      else state.areaShow[k] = !state.areaShow[k];
      render();
    }));
  }

  // what the recommendation is based on, in one paragraph
  function recNote(r) {
    const c = r.consistency, rec = r.areas.rec;
    if (!c || !c.ready || !rec) return "";
    const cr = c.cross || {};
    let t = `Worked out from ${nf(rec.plays, 0)} replays and ${nf(c.noiseHits || 0, 0)} notes, on ${cr.areas || 1} ${cr.areas === 1 ? "area" : "different areas"}`;
    t += cr.mapsMulti ? `, plus the comparison on ${mapsTxt(cr.mapsMulti)} replayed with different widths.` : ".";
    if (rec.curMiss >= 0 && rec.finalMiss >= 0 && Math.abs(rec.curMiss - rec.finalMiss) >= 0.05) t += ` Expected aim misses: ${nf(rec.finalMiss, 1)} per 1000 notes with the suggested area, ${nf(rec.curMiss, 1)} with the current one.`;
    if (rec.edge === "up") t += ` You haven't played enough above ${nf(rec.exploredHi, 1)} mm yet: I widen by at most 6% at a time.`;
    if (rec.edge === "down") t += ` You haven't played enough below ${nf(rec.exploredLo, 1)} mm yet: I narrow by at most 6% at a time.`;
    return `<p class="small muted" style="margin-top:10px">${esc(t)} <a href="#/consistency">Details</a></p>`;
  }

  /* ----- Aim ----- */
  function pageAim(view) {
    const r = state.report;
    const eps = (r && r.aimEpochs) || [];
    if (!r || !eps.length) {
      view.innerHTML = `<div class="page">${needsData("More data needed", "Play a few maps: the aim analysis appears after about 300 notes hit with the same area.")}</div>`;
      return;
    }
    const m = eps.find((e) => e.epochId === state.aimEpoch) || eps[0];
    const curEp = eps.find((e) => e.current);
    const period = (e) => `from ${dateFmt.format(d(e.from))}${e.to ? ` to ${dateFmt.format(d(e.to))}` : ""}`;
    let banner = "";
    if (!m.current && !curEp) banner = `I still have few notes with the current area: here is the data of the ${nfTrim(m.area.w, 1)} × ${nfTrim(m.area.h, 1)} mm area, used ${period(m)}. As soon as I have enough I switch to the current one.`;
    else if (!m.current) banner = `You're looking at the ${nfTrim(m.area.w, 1)} × ${nfTrim(m.area.h, 1)} mm area, used ${period(m)}.`;
    // few areas: buttons; many areas: a menu, newest first
    const epLabel = (e) => `${nfTrim(e.area.w, 1)} × ${nfTrim(e.area.h, 1)} mm${e.current ? " · current" : ` · from ${dateFmt.format(d(e.from))}`} · ${nf(e.hits, 0)} notes`;
    const picker = eps.length > 4
      ? `<div class="field" style="min-width:300px"><label for="aimEp">Area to analyse (${eps.length})</label><select class="input" id="aimEp">${eps.slice().sort((x, y) => (y.current ? 1 : 0) - (x.current ? 1 : 0) || +d(y.from) - +d(x.from)).map((e) => `<option value="${e.epochId}" ${e.epochId === m.epochId ? "selected" : ""}>${esc(epLabel(e))}</option>`).join("")}</select></div>`
      : eps.length > 1 ? `<div class="seg" role="group" aria-label="Area to analyse">${eps.map((e) => `<button data-ep="${e.epochId}" class="${e.epochId === m.epochId ? "on" : ""}">${nfTrim(e.area.w, 1)} mm${e.current ? " (current)" : ""}</button>`).join("")}</div>` : "";
    const dir = (dx, dy) => {
      const h = dx >= 0 ? "to the right" : "to the left", v = dy >= 0 ? "down" : "up";
      const ax = Math.abs(dx), ay = Math.abs(dy);
      if (ay < ax * 0.25) return h;
      if (ax < ay * 0.25) return v;
      return v + " and " + h;
    };
    const off = Math.hypot(m.offsetX, m.offsetY);
    const drift = m.drift || [];
    view.innerHTML = `<div class="page">
      <div class="row" style="justify-content:space-between;align-items:flex-start"><div><h1 class="h-page">How you aim</h1>
      <p class="lede">Every measure is in millimetres on the tablet, with the area shown. Small errors are normal: what matters is what always repeats the same way.</p></div>${picker}</div>
      ${banner ? `<div class="note-bar">${ICON.info}<span>${esc(banner)}</span></div>` : ""}
      <div class="stats" style="margin-top:22px">
        <div class="stat"><div class="k">Average offset</div><div class="v readout">${nf(off, 2)}<span class="unit">mm</span></div><div class="h">${off >= 0.1 ? dir(...userVec(m.offsetX, m.offsetY, m.area && m.area.rot)) : "centred"} · ± ${nf(m.offsetErr, 2)} mm</div></div>
        <div class="stat"><div class="k">On jumps you land</div><div class="v readout">${signed(m.jumpGain, 1)}%</div><div class="h">${m.jumpGain >= 0 ? "long" : "short"} · ± ${nf(m.jumpGainErr, 1)}%</div></div>
        <div class="stat"><div class="k">Movement rotation</div><div class="v readout">${signed(m.rotation, 1)}°</div><div class="h">± ${nf(m.rotationErr, 1)}°</div></div>
        <div class="stat"><div class="k">Clicks inside the circle</div><div class="v readout">${pct(m.inside, 1)}</div><div class="h">${nf(m.hits, 0)} notes with this area</div></div>
      </div>
      <section class="section">${sectionHead("Where you land on the tablet", "Each arrow is the average error in that zone, magnified 4 times. Arrows all alike mean a position problem; arrows pointing outwards or inwards mean a size problem.")}
        <div class="chart" id="fieldTablet"></div>
      </section>
      <div class="grid-2 section">
        <div class="panel"><h3>Long or short by jump length</h3><p class="sub">Error along the direction of the movement. Above zero you overshoot the circle.</p><div class="chart" id="distChart"></div></div>
        <div class="panel"><h3>By direction</h3><p class="sub">How far you land long (outside the green circle) or short, for every jump direction.</p><div class="chart" id="polarChart"></div></div>
      </div>
      <div class="grid-2 section">
        <div class="panel"><h3>Your clicks against the circle</h3><p class="sub">Each dot is a click, rotated so the jump comes from the left. The blue circle is the edge of the note.</p><div class="chart" id="targetChart"></div><div class="legend-row"><span><i class="dot" style="--c:var(--c1)"></i>click inside</span><span><i class="dot" style="--c:var(--bad)"></i>click outside</span><span><i class="dot" style="--c:var(--next)"></i>mean point</span></div></div>
        <div class="panel"><h3>During the map</h3><p class="sub">If the offset changes between the start and the end, your hand is sliding on the tablet.</p>
          ${drift.length ? `<table class="data"><thead><tr><th>Part of the map</th><th class="num">Horizontal</th><th class="num">Vertical</th><th class="num">Notes</th></tr></thead><tbody>${drift.map((x) => `<tr><td>${esc(x.stage)}</td><td class="num">${signed(x.ex, 2)} mm</td><td class="num">${signed(x.ey, 2)} mm</td><td class="num">${nf(x.n, 0)}</td></tr>`).join("")}</tbody></table>
          <p class="small muted" style="margin-top:12px">${driftText(drift)}</p>` : `<p class="muted small">Not enough data yet.</p>`}
        </div>
      </div>
    </div>`;
    const a = r.areas;
    Charts.draw($("#fieldTablet"), Charts.tablet, { tw: a.tabletW, th: a.tabletH, areas: [{ rect: m.area, kind: "now", label: m.current ? "current" : `${nfTrim(m.area.w, 1)} mm` }], playfield: movePlayfield(a.playfield, a.current, m.area), field: m.field, fieldScale: 4, maxHeight: 460, zoom: true });
    $$("[data-ep]", view).forEach((b) => b.addEventListener("click", () => { state.aimEpoch = +b.dataset.ep; render(); }));
    const mSel = $("#aimEp");
    if (mSel) mSel.addEventListener("change", () => { state.aimEpoch = +mSel.value; render(); });
    const dist = m.distance || [];
    if (dist.length >= 2) {
      const g = m.jumpGain / 100;
      Charts.draw($("#distChart"), Charts.line, {
        x: dist.map((p) => p.d), height: 250, xLabel: "jump length (mm)", yLabel: "mm", zero: true,
        yFmt: (v) => nf(v, 1), tipFmt: (v) => nf(v, 2) + " mm", tipX: (x) => `Jumps of ${nf(x, 0)} mm`,
        series: [
          { name: "measured", values: dist.map((p) => p.mean), lo: dist.map((p) => p.lo), hi: dist.map((p) => p.hi), color: "var(--c1)", dots: true },
          { name: `trend (${signed(m.jumpGain, 1)}%)`, values: dist.map((p) => p.d * g), color: "var(--text-3)", dash: true, width: 1.5 },
        ],
      });
    }
    if ((m.directions || []).length >= 4) Charts.draw($("#polarChart"), Charts.polar, { points: m.directions, height: 300 });
    else $("#polarChart").innerHTML = `<p class="muted small">More jumps in every direction are needed.</p>`;
    const pts = m.scatter || [];
    let mx = 0, my = 0;
    for (const p of pts) { mx += p.x; my += p.y; }
    Charts.draw($("#targetChart"), Charts.target, { points: pts, limit: 2, height: 320, mean: pts.length ? { x: mx / pts.length, y: my / pts.length } : null });
  }

  // the playfield drawn on another area: same position relative to the area
  function movePlayfield(pf, from, to) {
    if (!pf || !from || !to) return pf;
    const rf = ((from.rot || 0) * Math.PI) / 180, rt = ((to.rot || 0) * Math.PI) / 180;
    return pf.map(([x, y]) => {
      const dx = x - from.x, dy = y - from.y;
      const lx = (Math.cos(rf) * dx + Math.sin(rf) * dy) / from.w, ly = (-Math.sin(rf) * dx + Math.cos(rf) * dy) / from.h;
      const ux = lx * to.w, uy = ly * to.h;
      return [to.x + Math.cos(rt) * ux - Math.sin(rt) * uy, to.y + Math.sin(rt) * ux + Math.cos(rt) * uy];
    });
  }

  function driftText(dr) {
    if (dr.length < 2) return "";
    const a = dr[0], b = dr[dr.length - 1];
    const dx = b.ex - a.ex, dy = b.ey - a.ey;
    const l = Math.hypot(dx, dy);
    if (l < 0.3) return "The position stays steady for the whole map: your hand doesn't slide.";
    return `From the start to the end of the map the offset changes by ${nf(l, 1)} mm: your hand probably slides during long maps. Try resting your wrist on the same spot every time.`;
  }

  /* ----- Same maps, different areas ----- */
  const mapsTxt = (n) => (n === 1 ? "1 map" : `${n} maps`);
  // a same-map width curve in a few words
  function curveText(e) {
    if (!e) return "";
    if (e.verdict === "bigger" || e.verdict === "smaller") return `on the same maps you miss least with about ${nf(e.bestW, 1)} mm: error ${signed(e.gain * 100, 0)}% against ${nf(e.refW, 1)} mm (${mapsTxt(e.maps)})`;
    if (e.verdict === "current") return `among the widths tried, ${nf(e.refW, 1)} mm is the best (${mapsTxt(e.maps)})`;
    return `no sure difference between the widths tried, from ${nf(e.widthMin, 1)} to ${nf(e.widthMax, 1)} mm (${mapsTxt(e.maps)})`;
  }
  const SOURCE_LABEL = { model: "from the precision model", combined: "model + same maps", evidence: "decided by the same maps", "evidence-step": "one step towards the model", confirmed: "confirmed by the same maps" };
  function decisionText(c, r) {
    const z = c.size, cr = c.cross || {}, rec = r.areas && r.areas.rec;
    if (!z) return "";
    const fw = r.areas && r.areas.final ? r.areas.final.w : z.finalW;
    let t;
    switch (z.source) {
      case "combined":
        t = `The model points to ${nf(z.modelW, 1)} mm; on the replayed maps you miss least around ${nf(z.empW, 1)} mm (between ${nf(z.empLo, 1)} and ${nf(z.empHi, 1)}). The suggestion combines them, giving more weight to the more precise one: ${nf(fw, 1)} mm.`;
        break;
      case "evidence":
        t = `The model would point to ${nf(z.modelW, 1)} mm, but on the same maps with that width you missed ${nf(z.evidRel * 100, 0)}% more than with ${nf(z.evidW, 1)} mm. The real data wins: ${nf(fw, 1)} mm.`;
        break;
      case "evidence-step":
        t = `The model would point to ${nf(z.modelW, 1)} mm, but on the same maps the widths tried in that direction did worse than ${nf(z.evidW, 1)} mm. The suggestion takes only one step towards the model (${nf(fw, 1)} mm), to check it against the data.`;
        break;
      case "confirmed":
        t = `The model points to ${nf(z.modelW, 1)} mm and the replayed maps confirm it: it's the width with which you missed least.`;
        break;
      default:
        t = `The suggestion comes from the precision model, built from the notes of every area you used.${cr.mapsMulti ? " The replayed maps don't yet point surely to a different width, so they don't correct it." : ""}`;
    }
    if (rec && rec.testing) t += " It's on trial now: it stays put until you've played enough with it.";
    else if (rec && rec.kept) t += " It hasn't changed since last time: the difference from the new calculation is within the margin of uncertainty, so it doesn't chase a single map's fluke.";
    return t;
  }
  function sameMapTable(cr, curW) {
    const rows = [];
    if (cr.combined) rows.push(cr.combined);
    for (const e of cr.effects || []) if (e.group === "cs") rows.push(e);
    for (const e of cr.effects || []) if (e.group === "cat") rows.push(e);
    if (!rows.length) return "";
    const bins = [...new Set(rows.flatMap((e) => e.points.map((p) => p.bin)))].sort((a, b) => (cr.binW[a] || 0) - (cr.binW[b] || 0));
    const curBin = bins.reduce((best, b) => (best == null || Math.abs(cr.binW[b] - curW) < Math.abs(cr.binW[best] - curW) ? b : best), null);
    const head = bins.map((b) => `<th class="num">${nf(cr.binW[b], 1)} mm${b === curBin && Math.abs(cr.binW[b] - curW) <= curW * 0.02 ? '<br><span class="tag cur">current</span>' : ""}</th>`).join("");
    const body = rows.map((e) => {
      const cells = bins.map((b) => {
        const p = e.points.find((x) => x.bin === b);
        if (!p) return `<td class="num muted">–</td>`;
        if (p.ref) return `<td class="num"><span class="sm ref" title="${esc(mapsTxt(p.maps))}">reference</span></td>`;
        const cls = p.hi < 0 ? "good" : p.lo > 0 ? "bad" : "faint";
        const tip = `${signed(p.rel * 100, 0)}% (between ${signed(p.lo * 100, 0)}% and ${signed(p.hi * 100, 0)}%) · ${mapsTxt(p.maps)}, ${p.plays} plays`;
        return `<td class="num"><span class="sm ${cls}" title="${esc(tip)}">${signed(p.rel * 100, 0)}%</span></td>`;
      }).join("");
      const name = e.group === "all" ? "<b>All notes</b>" : e.group === "cs" ? esc(e.name) : `<span class="muted">${esc(e.name)}</span>`;
      return `<tr><td>${name}<div class="xs muted">${mapsTxt(e.maps)}</div></td>${cells}</tr>`;
    }).join("");
    return `<div class="panel" style="margin-top:16px;overflow-x:auto"><table class="data smtable"><thead><tr><th>Notes</th>${head}</tr></thead><tbody>${body}</tbody></table>
      <p class="xs muted" style="margin-top:10px">Error on the same maps against the reference width (the current one or the closest tried). Green: you clearly miss less; red: clearly more; grey: difference not yet sure. Close widths (within 2.5%) count as one.</p></div>`;
  }

  /* ----- Filter ----- */
  const RF_FIELDS = [
    ["outer", "Outer Radius", "mm", 2],
    ["inner", "Inner Radius", "mm", 2],
    ["coef", "Initial Smoothing Coefficient", "", 2],
    ["knee", "Soft Knee Scale", "", 2],
    ["leak", "Smoothing Leak Coefficient", "", 2],
  ];
  function rfTable(p, cur, foot) {
    const cells = RF_FIELDS.map(([k, name, u, dd]) => {
      const v = p[k];
      const old = cur ? cur[k] : null;
      const ch = cur && Math.abs(v - old) >= 0.005;
      return `<div class="otd-cell ${ch ? "changed" : ""}"><div class="k">${esc(name)}</div><div class="v readout">${nfTrim(v, dd)}${u ? `<span class="unit">${u}</span>` : ""}</div><div class="d">${cur ? (ch ? `now ${nfTrim(old, dd)}` : "same") : ""}</div><button class="copy" data-copy="${nfTrim(v, dd)}" aria-label="Copy ${esc(name)}">copy</button></div>`;
    }).join("");
    const txt = RF_FIELDS.map(([k, name, u, dd]) => `${name}: ${nfTrim(p[k], dd)}${u ? " " + u : ""}`).join("\n");
    return `<div class="otd"><div class="otd-head"><span>AbstractQbit's Radial Follow Smoothing (Tablet coordinates)</span><button class="btn small ghost" data-copy="${esc(txt)}">${ICON.copy}Copy all</button></div><div class="otd-grid rf">${cells}</div><div class="otd-foot">${foot || "In OpenTabletDriver: Filters → add <b>AbstractQbit's Radial Follow Smoothing (Tablet coordinates)</b> from the Plugin Manager, type in the values and press <b>Save</b>. The program records the change by itself and compares the real data before and after."}</div></div>`;
  }
  function gainTxt(e) {
    if (Math.abs(e.gain) < 0.05) return "no difference";
    const share = e.missRaw > 0 ? ` (${nf((e.gain / e.missRaw) * 100, 0)}% of the misses)` : "";
    return e.gain > 0 ? `${nf(e.gain, 1)} more clicks in the circle per 1000${share}` : `${nf(-e.gain, 1)} fewer clicks in the circle per 1000`;
  }
  function pageFilter(view) {
    const f = state.filter;
    if (!f) { view.innerHTML = `<div class="page">${needsData("Filter", "I can't read the state of the filter study.")}</div>`; return; }
    const res = f.result;
    const rateSel = `<div class="field" style="max-width:320px"><label for="rfRate">Tablet report rate</label><select class="input" id="rfRate">${[133, 200, 266, 300, 500, 1000].map((v) => `<option value="${v}" ${Math.abs(f.rate - v) < 0.5 ? "selected" : ""}>${v} Hz${f.rateAuto && Math.abs(f.rate - v) < 0.5 ? " (estimated)" : ""}</option>`).join("")}</select><p class="xs muted" style="margin-top:6px">The filter works on every pen report: at a different rate the same value has a different effect. Wacom CTL/CTH: 133 Hz; Wacom Intuos Pro: 200 Hz; many XP-Pen and Huion: 266 Hz.</p></div>`;
    const active = f.active ? `<div class="note-bar">${ICON.info}<span>Radial Follow is on in OpenTabletDriver now (${f.active.tablet ? "tablet coordinates" : "screen, in pixels"}): outer ${nfTrim(f.active.outer, 2)}, inner ${nfTrim(f.active.inner, 2)}, coefficient ${nfTrim(f.active.coef, 2)}, knee ${nfTrim(f.active.knee, 2)}, leak ${nfTrim(f.active.leak, 2)}.${f.filtered ? " Some replays were recorded with a filter already on: the simulation starts from those already filtered movements." : ""}</span></div>` : (f.others && f.others.length ? `<div class="note-bar">${ICON.info}<span>Filters on in OpenTabletDriver: ${esc(f.others.join(", "))}.</span></div>` : "");
    let body = "";
    if (f.running) {
      body = `<div class="panel" style="margin-top:22px"><h3>${esc(f.stage || "Study running")}</h3><p class="sub">Every click of your replays is replayed through the filter, with hundreds of different settings.</p><div class="progress"><div class="track"><span style="width:${Math.round((f.progress || 0) * 100)}%"></span></div><div class="cap"><span>Progress</span><span>${pct(f.progress || 0)}</span></div></div></div>`;
    } else if (f.error && !res) {
      body = `<div class="panel" style="margin-top:22px"><h3>I can't suggest a filter yet</h3><p class="sub">${esc(f.error)}</p><button class="btn small" id="rfRun" style="margin-top:10px">Try again</button></div>`;
    } else if (res) {
      const opts = res.options || [];
      const best = opts.find((o) => o.key === res.best);
      const cur = f.active && f.active.tablet ? f.active : null;
      const head = best
        ? `<p class="bigline" style="margin-top:10px">With your movements, this Radial Follow puts <b class="readout">${nf(best.eval.gain, 1)}</b> more clicks in the circle per 1000, adding on average <b class="readout">${nf(best.eval.delay, 1)} ms</b> to reach the circle.</p>${rfTable(best.eval.params, cur)}`
        : `<p class="bigline" style="margin-top:10px">With your movements no Radial Follow setting surely puts more clicks in the circle: I suggest not using it, or keeping it at the minimal values below.</p>${rfTable({ outer: 0, inner: 0, coef: 0.95, knee: 1, leak: 0 }, cur)}`;
      const d = res.diagnosis || {};
      const why = [];
      if (d.overshoot > 0.15) why.push(`In ${pct(d.overshoot, 0)} of the clicks the pen had gone past the circle and was coming back: smoothing makes the cursor follow those quick corrections more gently.`);
      if (d.tremor > 0) why.push(`Right before the click the pen moves on average ${nf(d.tremor, 2)} mm per report: that's the tremor the filter can absorb.`);
      if (d.moving > 0.2) why.push(`In ${pct(d.moving, 0)} of the clicks the pen was still moving fast: the more the filter holds the cursor back, the shorter those clicks land.`);
      body = `${head}
        <section class="section">${sectionHead("The levels", "Picked on half of your replays and checked on the other half, so the gain isn't inflated by chance. Delay: how much later the cursor enters the circle than the pen. Smoothest: the strongest smoothing, with the most delay. To know which works best for you, try them in game from the Diagnosis page.")}
          <div class="grid-2">${opts.map((o) => `<div class="panel opt ${o.key === res.best ? "best" : ""}"><h3>${esc(o.name)}${o.key === res.best ? ' <span class="tag cur">suggested</span>' : ""}</h3><p class="sub">at most ${nf(o.budget, 0)} ms of delay</p>
            <dl class="kv"><dt>Effect</dt><dd>${esc(gainTxt(o.eval))}</dd><dt>Range</dt><dd class="readout">${signed(o.eval.gainLo, 1)} … ${signed(o.eval.gainHi, 1)}</dd><dt>Average delay</dt><dd class="readout">${nf(o.eval.delay, 1)} ms</dd><dt>Values</dt><dd class="readout xs">${RF_FIELDS.map(([k]) => nfTrim(o.eval.params[k], 2)).join(" · ")}</dd></dl>
            ${o.sure ? "" : '<p class="xs muted" style="margin-top:8px">Gain not sure: it could be zero.</p>'}</div>`).join("")}</div>
        </section>
        <div class="grid-2 section">
          <div class="panel"><h3>Gain and delay</h3><p class="sub">The best settings tried for every delay: further left is snappier, higher up puts more clicks in the circle.</p><div class="chart" id="rfChart"></div></div>
          <div class="panel"><h3>How you move the pen when you click</h3><ul class="insights">${why.map((x) => `<li>${esc(x)}</li>`).join("") || "<li>Nothing unusual.</li>"}</ul>
            <dl class="kv" style="margin-top:12px"><dt>Clicks analysed</dt><dd class="readout">${nf(res.notes, 0)}</dd><dt>Replays</dt><dd class="readout">${nf(res.plays, 0)}</dd><dt>Different areas</dt><dd class="readout">${nf(res.areas, 0)}</dd><dt>Plugin default settings</dt><dd>${esc(gainTxt(res.default))}, ${nf(res.default.delay, 1)} ms</dd>${res.current ? `<dt>Your current settings</dt><dd>${esc(gainTxt(res.current))}, ${nf(res.current.delay, 1)} ms</dd>` : ""}</dl></div>
        </div>`;
    }
    const real = (f.real || []).filter((g) => g.plays > 0);
    view.innerHTML = `<div class="page">
      <div class="row" style="justify-content:space-between;align-items:flex-start"><div><h1 class="h-page">Radial Follow filter</h1>
      <p class="lede">I replay your real movements, taken from every replay with every area, through the AbstractQbit's Radial Follow (Tablet coordinates) filter, and look for the values that put the most clicks inside the circles with the least delay. It's a smoothing filter: it reduces pen jitter, at the cost of a little delay. For values tuned on how you actually play, use the guided filter on the Diagnosis page.</p></div>
      ${f.running ? "" : `<button class="btn small" id="rfRun">${res ? "Recalculate" : "Calculate"}</button>`}</div>
      ${f.stale && !f.running ? `<div class="note-bar">${ICON.info}<span>There are new replays since the last calculation: press Recalculate to update.</span></div>` : ""}
      ${active}
      ${body}
      <section class="section">${sectionHead("Real data", "Plays made with each filter setup, read from the OpenTabletDriver settings. After a few sessions with the new filter, here you see whether the simulation was right.")}
        ${real.length ? `<div class="table-scroll"><table class="data"><thead><tr><th>Filters</th><th class="num">Plays</th><th class="num">Notes</th><th class="num">Aim misses</th><th class="num">Average error</th></tr></thead><tbody>${real.map((g) => `<tr><td>${esc(g.label)}${g.current ? ' <span class="tag cur">now</span>' : ""}</td><td class="num">${nf(g.plays, 0)}</td><td class="num">${nf(g.notes, 0)}</td><td class="num">${nf(g.missPer1k, 1)} per 1000</td><td class="num">${pct(g.normErr, 1)}</td></tr>`).join("")}</tbody></table></div>` : '<p class="muted small">No plays yet.</p>'}
      </section>
      <section class="section">${sectionHead("Study settings", "")}${rateSel}
        <p class="xs muted" style="margin-top:10px">The simulation uses your movements as they are: it can't know how you would change with the filter on. That's why the levels keep the delay low and the page then compares the real data.</p></section>
    </div>`;
    const run = $("#rfRun");
    if (run) run.addEventListener("click", async () => { state.filter = await api("filter", { action: "run" }).catch((e) => { toast(e.message); return state.filter; }); render(); });
    const rs = $("#rfRate");
    if (rs) rs.addEventListener("change", async () => { state.filter = await api("filter", { rate: +rs.value, action: "run" }).catch(() => state.filter); render(); });
    if (res && $("#rfChart")) {
      const fr = (res.frontier || []).filter((e) => e.delay <= 16);
      if (fr.length >= 2) {
        const pts = (res.options || []).map((o, i) => ({ x: o.eval.delay, y: o.eval.gain, color: o.key === res.best ? "var(--next)" : ["var(--c1)", "var(--c3)", "var(--c2)", "var(--c4)"][i % 4] }));
        Charts.draw($("#rfChart"), Charts.line, {
          x: fr.map((e) => e.delay), height: 240, ml: 48, zero: true, xLabel: "average delay (ms)", yLabel: "more clicks per 1000",
          xMax: Math.max(...fr.map((e) => e.delay), ...pts.map((q) => q.x)) * 1.04, yMax: Math.max(...fr.map((e) => e.gain), ...pts.map((q) => q.y), 1) * 1.12,
          xFmt: (v) => nf(v, 1), yFmt: (v) => nf(v, 1), tipX: (x) => `${nf(x, 2)} ms`, tipFmt: (v) => nf(v, 1) + " per 1000",
          series: [{ name: "best settings", values: fr.map((e) => e.gain), color: "var(--c1)", dots: true, width: 1.5 }],
          points: pts,
        });
      } else $("#rfChart").innerHTML = '<p class="muted small">Too few points to show.</p>';
    }
  }

  /* ----- Consistency (error by size) ----- */
  let weightTimer = null;
  function pageConsistency(view) {
    const r = state.report;
    const c = r && r.consistency;
    if (!r || !c || !c.ready) {
      view.innerHTML = `<div class="page">${needsData("More data needed", "The consistency model needs at least a few hundred jumps with the current area.")}</div>`;
      return;
    }
    const a = r.areas;
    const rec = a.rec || {};
    const fin = a.final || a.current;
    // the widths around the range the advice may use
    const lo = c.trustLoW * 0.96, hi = c.trustHiW * 1.04;
    const idx = c.widths.map((w, i) => [w, i]).filter(([w]) => w >= lo - 1e-9 && w <= hi + 1e-9).map(([, i]) => i);
    const pick = (arr) => idx.map((i) => arr[i]);
    const widths = pick(c.widths);
    const miss = (p) => (p == null || p < 0 ? null : (1 - p) * 1000);
    const nearest = (w) => {
      let bi = -1, bd = Infinity;
      widths.forEach((x, i) => { const dd = Math.abs(x - w); if (dd < bd) { bd = dd; bi = i; } });
      return bd <= 0.6 ? bi : -1;
    };
    const sameW = Math.abs(fin.w - a.current.w) < 0.8;
    const lead = sameW
      ? `Your current area (${nfTrim(a.current.w, 1)} mm) is already the one with which you miss least on the maps you play.`
      : `The area with which you miss least on the maps you play is <b class="readout">${nf(fin.w, 1)} × ${nf(fin.h, 1)} mm</b>${fin.errW ? ` (± ${nf(fin.errW, 1)} mm)` : ""}. The current one is ${nf(a.current.w, 1)} mm.`;
    const cats = c.categories || [];
    const trustShade = [];
    if (c.trustLoW > widths[0] + 0.3) trustShade.push({ x0: widths[0], x1: c.trustLoW, label: "not tried" });
    if (c.trustHiW < widths[widths.length - 1] - 0.3) trustShade.push({ x0: c.trustHiW, x1: widths[widths.length - 1], label: "not tried" });
    const curM = rec.curMiss >= 0 ? rec.curMiss : null, bestM = rec.finalMiss >= 0 ? rec.finalMiss : null;
    const weights = state.status?.settings?.weights || null;
    const cr = c.cross || {};
    const byCS = c.byCS || [];
    const csKeys = ["cs0", "cs1", "cs2", "cs3"];
    const csPanels = csKeys.map((k, i) => {
      const m = byCS.find((x) => x.key === k), e = (cr.effects || []).find((x) => x.key === k);
      if (!m && !e) return "";
      const name = (m || e).name;
      return `<div class="panel"><h3><span class="cat-chip" style="--c:${CS_COLORS[i]}"><i></i>${esc(name)}</span></h3>
        <p class="sub">${m ? `${pct(m.share)} of the notes · best on its own: ${nf(m.bestW, 1)} mm` : "few notes in the recent maps"}</p>
        ${m ? `<div class="chart" id="csm${i}"></div>` : ""}
        <p class="xs ${e && e.verdict !== "unclear" ? "" : "muted"}" style="margin-top:8px">${e ? esc(curveText(e).replace(/^./, (x) => x.toUpperCase())) + "." : "No map of this kind replayed with different areas."}</p></div>`;
    }).filter(Boolean);
    const adaptTxt = cr.adaptCostOk ? (cr.adaptCost > 0.01 ? `+${nf(cr.adaptCost * 100, 0)}%` : "negligible") : "–";
    const crossSection = `<section class="section">${sectionHead("Same maps, different areas", "The fairest comparison: each map is compared only with itself, played with different areas, taking into account the adaptation after each change and how much you improve over time. The suggested area uses these comparisons together with the model.")}
        <div class="stats" style="margin-top:8px">
          <div class="stat"><div class="k">Natural width of your hand</div><div class="v readout">${cr.naturalKnown ? nf(cr.naturalW, 1) : "–"}<span class="unit">mm</span></div><div class="h">${cr.naturalKnown ? `± ${nf(cr.naturalWErr, 1)} mm · from ${cr.areas} ${cr.areas === 1 ? "area" : "areas"}` : "more notes needed"}</div></div>
          <div class="stat"><div class="k">Maps replayed with different areas</div><div class="v readout">${nf(cr.mapsMulti || 0, 0)}</div><div class="h">${cr.curMaps ? `${cr.curMaps} also with the current area` : "none yet with the current area"}</div></div>
          <div class="stat"><div class="k">Right after a change</div><div class="v readout">${adaptTxt}</div><div class="h">extra error in the first plays</div></div>
          <div class="stat"><div class="k">Suggested area</div><div class="v readout">${a.final ? nf(a.final.w, 1) : "–"}<span class="unit">mm</span></div><div class="h">${c.size ? esc(SOURCE_LABEL[c.size.source] || "") : ""}</div></div>
        </div>
        <p class="small muted" style="margin-top:14px;max-width:900px">${cr.persistKnown
          ? `Comparing the areas you used I see how much you adapt: even once you got used to it, about ${nf(cr.persist * 100, 0)}% of the difference between the area and the natural size of your hand remains (± ${nf(cr.persistErr * 100, 0)}%). That's why the natural width is worked out as the point where that difference disappears, instead of chasing the last area used.`
          : "Once you've played with areas of different widths (at least 4 mm apart) I'll also measure how much you adapt to each area; for now I use a typical value."}</p>
        ${c.size ? `<div class="note-bar">${ICON.info}<span>${esc(decisionText(c, r))}</span></div>` : ""}
        ${cr.combined ? `<div class="panel" style="margin-top:16px"><h3>All notes, on the same maps</h3><p class="sub">Error against ${nf(cr.combined.refW, 1)} mm, with the margin of uncertainty. Below zero you miss less.</p><div class="chart" id="smChart"></div></div>${sameMapTable(cr, a.current.w)}`
          : `<div class="note-bar">${ICON.info}<span>${cr.mapsMulti ? "The maps replayed with different areas still have too few notes for a comparison." : "You haven't replayed a map with two different areas yet. It happens by itself when you replay the maps you like."}</span></div>`}
      </section>
      ${csPanels.length ? `<section class="section">${sectionHead("By circle size", "With small circles every millimetre of imprecision weighs more, with big circles speed matters more. The chart is the model's prediction for the notes of that CS; below, what the replayed maps say.")}<div class="grid-4">${csPanels.join("")}</div></section>` : ""}`;
    view.innerHTML = `<div class="page">
      <h1 class="h-page">Size and consistency</h1>
      <p class="bigline" style="margin-top:10px">${lead}</p>
      <p class="lede">The prediction uses the way you miss, measured on the replays: how much the pen wobbles, how much the error grows with distance and how much it gets worse with speed. ${(c.cross || {}).persistKnown ? `It takes into account that, even after getting used to it, about ${nf(c.cross.persist * 100, 0)}% of the difference between the area and the natural scale of your hand remains (measured by comparing your areas). ` : "It assumes that after adapting about a third of the difference between the area and the natural scale of your hand remains (a typical value, until I measure it on your areas). "}${c.noiseAreas > 1 ? `The precision of the hand doesn't depend on the area, so I use the notes of all ${c.noiseAreas} areas you used (${nf(c.noiseHits, 0)} notes): the more different areas you've tried, the more reliable the prediction. ` : ""}${c.lambdaKnown ? "" : "I don't have enough notes yet to know how far your hand reaches: for now the prediction only considers precision. "}${curM != null && bestM != null ? (!sameW ? `Expected aim misses, with the position corrected: ${nf(curM, 1)} per 1000 notes with the current area, ${nf(bestM, 1)} with the suggested one.` : `Expected aim misses with the position corrected: ${nf(bestM, 1)} per 1000 notes.`) : ""}</p>
      <section class="section">${sectionHead("Expected aim misses by area width", "Lower is better. Sum of every kind of pattern, weighted by how often it shows up in the maps you play; the band is the uncertainty. The grey zones are widths too far from the ones you tried: I don't suggest anything there.")}
        <div class="panel"><div class="chart" id="combChart"></div></div>
      </section>
      <section class="section">${sectionHead("By pattern type", "Each kind of pattern, on its own, would want a different width: the suggested area is the one with the fewest misses adding them all up.")}
        <div class="grid-4">${cats.map((ct, i) => `<div class="panel"><h3><span class="cat-chip" style="--c:${CAT_COLORS[i]}"><i></i>${esc(ct.name)}</span></h3><p class="sub">${ct.curve ? `best on its own: ${nf(ct.bestW, 1)} mm · ${pct(ct.share)} of the notes` : "few notes of this kind"}</p><div class="chart" id="cat${i}"></div></div>`).join("")}</div>
      </section>
      ${crossSection}
      <div class="grid-2 section">
        <div class="panel"><h3>How much it matters to you</h3><p class="sub">By default the weight is how often you meet each kind of pattern in the maps you play. Change it if you want to focus on something.</p>
          <div class="weights">${cats.map((ct, i) => `<div class="weight"><span class="cat-chip" style="--c:${CAT_COLORS[i]}"><i></i>${esc(ct.name)}</span><input type="range" min="0" max="100" step="1" value="${Math.round((ct.weight || 0) * 100)}" data-cat="${["fast", "wide", "stream", "small"].indexOf(ct.key)}" aria-label="Weight of ${esc(ct.name)}"><output>${Math.round((ct.weight || 0) * 100)}</output></div>`).join("")}</div>
          <div class="row" style="margin-top:14px"><button class="btn small" id="resetWeights" ${weights ? "" : "disabled"}>Use the maps I play</button><span class="xs muted">${weights ? "Custom weights" : "Automatic weights"}</span></div>
        </div>
        <div class="panel"><h3>Your precision profile</h3><p class="sub">Pen scatter on a 40 mm jump, by the speed required.</p>
          <dl class="kv" style="margin-bottom:10px"><dt>Base wobble</dt><dd class="readout">${nf(c.steadiness, 2)} mm</dd><dt>Growth with distance</dt><dd class="readout">${nf(c.distTerm, 1)}% of the jump</dd><dt>Top speed reached</dt><dd class="readout">${nf(c.vmax, 2)} m/s</dd></dl>
          <div class="chart" id="sigmaChart"></div>
        </div>
      </div>
      ${(c.insights || []).length ? `<section class="section">${sectionHead("What the data says", "")}<ul class="insights">${c.insights.map((x) => `<li>${esc(x)}</li>`).join("")}</ul></section>` : ""}
      ${(c.epochs || []).length >= 1 ? `<section class="section">${sectionHead("Comparing the areas you used", "Average error actually seen (not predicted) with each area, by pattern type. Further left is better; the bars are the confidence interval. Keep in mind you may also have improved in the meantime.")}<div class="panel"><div class="chart" id="epochChart"></div><div class="legend-row" id="epochLegend"></div></div><div id="naturalTable" style="margin-top:18px"></div></section>` : ""}
      ${(c.calibration || []).length ? `<section class="section">${sectionHead("Is the model reliable?", "For groups of notes, the share of clicks inside the circle predicted by the model and the one seen. If the dots sit on the diagonal, the predictions are well calibrated.")}<div class="panel" style="max-width:560px"><div class="chart" id="calChart"></div></div></section>` : ""}
    </div>`;
    const vm = [{ x: a.current.w, label: "current", color: "var(--now)", dash: true }];
    if (!sameW) vm.push({ x: fin.w, label: "suggested", color: "var(--next)", row: 1 });
    Charts.draw($("#combChart"), Charts.line, {
      x: widths, height: 280, xLabel: "area width (mm)", yLabel: "misses per 1000 notes", shade: trustShade, yMin: 0,
      yFmt: (v) => nf(v, v < 10 ? 1 : 0), tipFmt: (v) => nf(v, 1) + " per 1000", tipX: (x) => `${nf(x, 1)} mm`, xFmt: (v) => nf(v, 0),
      series: [{ name: "expected misses", values: pick(c.combined).map(miss), lo: pick(c.combHi).map(miss), hi: pick(c.combLo).map(miss), color: "var(--c1)" }],
      vmarks: vm, ml: 54,
    });
    cats.forEach((ct, i) => {
      const el = $("#cat" + i);
      if (!ct.curve) { el.innerHTML = `<p class="muted xs">Play maps with this kind of pattern to see the curve.</p>`; return; }
      const bi = nearest(ct.bestW);
      Charts.draw(el, Charts.line, {
        x: widths, height: 170, ml: 40, xFmt: (v) => nf(v, 0), yTicks: 3, yMin: 0,
        yFmt: (v) => nf(v, v < 10 ? 1 : 0), tipFmt: (v) => nf(v, 1) + " per 1000", tipX: (x) => `${nf(x, 1)} mm`,
        series: [{ name: ct.name, values: pick(ct.curve).map(miss), lo: pick(ct.hi).map(miss), hi: pick(ct.lo).map(miss), color: CAT_COLORS[i] }],
        vmarks: [{ x: a.current.w, color: "var(--now)", dash: true, width: 1 }],
        points: bi >= 0 ? [{ x: widths[bi], y: miss(pick(ct.curve)[bi]), color: CAT_COLORS[i] }] : [],
      });
    });
    byCS.forEach((m) => {
      const i = csKeys.indexOf(m.key), el = $("#csm" + i);
      if (!el) return;
      const bi = nearest(m.bestW);
      Charts.draw(el, Charts.line, {
        x: widths, height: 150, ml: 40, xFmt: (v) => nf(v, 0), yTicks: 3, yMin: 0,
        yFmt: (v) => nf(v, v < 10 ? 1 : 0), tipFmt: (v) => nf(v, 1) + " per 1000", tipX: (x) => `${nf(x, 1)} mm`,
        series: [{ name: m.name, values: pick(m.curve).map(miss), color: CS_COLORS[i] }],
        vmarks: [{ x: a.current.w, color: "var(--now)", dash: true, width: 1 }],
        points: bi >= 0 ? [{ x: widths[bi], y: miss(pick(m.curve)[bi]), color: CS_COLORS[i] }] : [],
      });
    });
    if (cr.combined && $("#smChart")) {
      const pts = cr.combined.points.slice().sort((p, q) => p.w - q.w);
      const xs = pts.map((p) => p.w);
      const pad = Math.max(1.5, (xs[xs.length - 1] - xs[0]) * 0.08);
      const vm2 = [{ x: a.current.w, label: "current", color: "var(--now)", dash: true }];
      if (a.final && Math.abs(a.final.w - a.current.w) >= 0.3) vm2.push({ x: a.final.w, label: "suggested", color: "var(--next)", row: 1 });
      Charts.draw($("#smChart"), Charts.line, {
        x: xs, xMin: Math.min(xs[0], a.current.w, fin.w) - pad, xMax: Math.max(xs[xs.length - 1], a.current.w, fin.w) + pad, height: 240, ml: 48, zero: true,
        xLabel: "area width (mm)", yLabel: "error against the reference", xFmt: (v) => nf(v, 0), yFmt: (v) => signed(v, 0) + "%",
        tipX: (x, i) => `${nf(x, 1)} mm · ${mapsTxt(pts[i].maps)}, ${pts[i].plays} plays`, tipFmt: (v, se, i) => pts[i].ref ? "reference" : `${signed(v, 1)}% (between ${signed(pts[i].lo * 100, 0)}% and ${signed(pts[i].hi * 100, 0)}%)`,
        series: [{ name: "same maps", values: pts.map((p) => p.rel * 100), lo: pts.map((p) => p.lo * 100), hi: pts.map((p) => p.hi * 100), color: "var(--c1)", dots: true, bars: true, width: 1.5 }],
        vmarks: vm2,
      });
    }
    const sc = c.sigmaCurve || [];
    if (sc.length) Charts.draw($("#sigmaChart"), Charts.line, {
      x: sc.map((p) => p[0]), height: 200, xLabel: "speed required (m/s)", yLabel: "mm", ml: 44, yMin: 0,
      xFmt: (v) => nf(v, 2), yFmt: (v) => nf(v, 1), tipFmt: (v) => nf(v, 2) + " mm", tipX: (x) => `${nf(x, 2)} m/s`,
      series: [{ name: "along the jump", values: sc.map((p) => p[1]), color: "var(--c2)" }, { name: "sideways", values: sc.map((p) => p[2]), color: "var(--c3)" }],
      shade: c.vmax ? [{ x0: c.vmax, x1: sc[sc.length - 1][0], label: "never reached" }] : [], directLabels: true, mr: 92,
    });
    $$(".weights input", view).forEach((inp) => inp.addEventListener("input", () => {
      inp.nextElementSibling.textContent = inp.value;
      clearTimeout(weightTimer);
      weightTimer = setTimeout(async () => {
        const w = [0, 0, 0, 0, 0];
        $$(".weights input", view).forEach((x) => (w[+x.dataset.cat] = +x.value / 100));
        try { await api("settings", { weights: w }); toast("Weights saved: recalculating the suggested area"); await refreshStatus(); } catch (e) { toast(e.message); }
      }, 500);
    }));
    const rb = $("#resetWeights");
    if (rb) rb.addEventListener("click", async () => { await api("settings", { weights: null }); toast("Back to automatic weights"); refreshStatus(); });
    if ((c.epochs || []).length) {
      // with many areas show the current one and the 5 with the most notes
      const allEps = c.epochs;
      const eps = allEps.length <= 6 ? allEps : allEps.filter((e) => e.current).concat(allEps.filter((e) => !e.current).sort((x, y) => y.hits - x.hits).slice(0, 5));
      const epName = (e) => (e.unknown ? "unknown area" : `${nfTrim(e.area.w, 1)} mm`);
      const epColors = (e, i) => (e.current ? "var(--now)" : e.unknown ? "var(--c5)" : ["var(--c3)", "var(--c2)", "var(--c4)"][i % 3]);
      const rows = [];
      for (let k = 0; k < 4; k++) {
        rows.push({ label: cats[k] ? cats[k].name : "", items: eps.map((e, i) => [e, i]).filter(([e]) => e.byCat[k].aimed >= 60).map(([e, i]) => ({ v: e.byCat[k].normErr * 100, lo: e.byCat[k].normErrLo * 100, hi: e.byCat[k].normErrHi * 100, color: epColors(e, i), name: epName(e), extra: ["clicks inside", pct(e.byCat[k].inside, 1)] })) });
      }
      rows.push({ label: "All", items: eps.map((e, i) => ({ v: e.byCat[5].normErr * 100, lo: e.byCat[5].normErrLo * 100, hi: e.byCat[5].normErrHi * 100, color: epColors(e, i), name: epName(e), extra: ["clicks inside", pct(e.byCat[5].inside, 1)] })) });
      Charts.draw($("#epochChart"), Charts.dots, { rows, ml: 130, xLabel: "average error (% of the radius)", fmt: (v) => nf(v, 0) + "%" });
      $("#epochLegend").innerHTML = eps.map((e, i) => `<span><i class="dot" style="--c:${epColors(e, i)}"></i>${e.unknown ? "Unknown area" : `${nfTrim(e.area.w, 1)} × ${nfTrim(e.area.h, 1)} mm`}${e.current ? " (current)" : ""} · ${nf(e.hits, 0)} notes</span>`).join("") + (allEps.length > eps.length ? `<span class="muted">Showing the current area and the 5 with the most notes, out of ${allEps.length}.</span>` : "");
      const nat = allEps.filter((e) => e.hasBias);
      if (nat.length) {
        $("#naturalTable").innerHTML = `<div class="table-scroll"><table class="data"><thead><tr><th>Area</th><th class="num">Notes</th><th class="num">On jumps you land</th><th class="num">Average offset</th><th class="num">Natural scale</th><th class="num">Natural width</th></tr></thead><tbody>${nat.map((e) => `<tr><td>${nfTrim(e.area.w, 1)} × ${nfTrim(e.area.h, 1)} mm${e.current ? ' <span class="tag cur">current</span>' : ""}</td><td class="num">${nf(e.hits, 0)}</td><td class="num">${e.jumpGain >= 0 ? "+" : "−"}${nf(Math.abs(e.jumpGain), 1)}%</td><td class="num">${nf(e.offset, 2)} mm</td><td class="num">${nf(e.lambda * 100, 1)}%</td><td class="num readout">${nf(e.naturalW, 1)} mm</td></tr>`).join("")}</tbody></table></div>
        <p class="xs muted" style="margin-top:8px">The natural width is the width that matches the movement of your hand while you were using that area. If it stays similar across different areas, your hand has a stable preferred size; if it follows the area, you adapt easily and precision is what matters most.</p>`;
      }
    }
    const cal = c.calibration || [];
    if (cal.length) {
      const lo = Math.min(...cal.map((p) => Math.min(p[0], p[1]))) * 100;
      Charts.draw($("#calChart"), Charts.line, {
        x: cal.map((p) => p[0] * 100), height: 240, xMin: Math.floor(lo), xMax: 100, yMin: Math.floor(lo), yMax: 100, ml: 48,
        xLabel: "predicted (%)", xFmt: (v) => nf(v, 100 - lo < 6 ? 1 : 0), yFmt: (v) => nf(v, 100 - lo < 6 ? 1 : 0), tipFmt: (v) => nf(v, 1) + "%", tipX: (x) => `predicted ${nf(x, 1)}%`,
        series: [{ name: "seen", values: cal.map((p) => p[1] * 100), color: "var(--c1)", dots: true, width: 1.5 }, { name: "perfect", values: cal.map((p) => p[0] * 100), color: "var(--text-3)", dash: true, width: 1 }],
      });
    }
  }

  /* ----- Progress ----- */
  function pageProgress(view) {
    const r = state.report;
    const p = r && r.progress;
    const ses = (p && p.sessions) || [];
    if (!r || ses.length < 2) {
      view.innerHTML = `<div class="page">${needsData("Still few sessions", "The progress charts appear after at least two play sessions (a session is a group of plays without long breaks).")}</div>`;
      return;
    }
    const ad = p.adaptation || {};
    const epochStarts = (r.areas.periods || r.areas.epochs || []).filter((e, i) => i > 0).map((e) => ({ t: +d(e.from), w: e.area.w }));
    const xs = ses.map((s) => +d(s.start));
    const vm = epochStarts.map((e) => ({ x: e.t, color: "var(--next)", dash: true, width: 1 }));
    // area periods as labelled bands
    const now = Date.now();
    const bands = (r.areas.periods || r.areas.epochs || []).map((e, i) => ({ x0: +d(e.from), x1: e.to ? +d(e.to) : now, label: `${nfTrim(e.area.w, 1)} mm`, fill: i % 2 === 1, strong: !e.to }));
    const dfmt = (t) => dateFmt.format(new Date(t));
    view.innerHTML = `<div class="page">
      <h1 class="h-page">Progress</h1>
      <p class="lede">Each dot is a session; the light line is the trend over nearby sessions. The bands show the area used in that period. Single sessions swing a lot depending on the maps played: look at the trend.</p>
      ${journey(r)}
      <div class="grid-2 section">
        <div class="panel"><h3>Average error</h3><p class="sub">Average distance of the click from the centre, in % of the radius. Lower is better.</p><div class="chart" id="pErr"></div></div>
        <div class="panel"><h3>Aim misses</h3><p class="sub">Clicks at the right time but outside the circle, per 1000 notes.</p><div class="chart" id="pMiss"></div></div>
      </div>
      <div class="grid-2 section">
        <div class="panel"><h3>Long or short on jumps</h3><p class="sub">Above zero you overshoot the circle. The ideal value is close to zero and steady.</p><div class="chart" id="pGain"></div></div>
        <div class="panel"><h3>Average offset</h3><p class="sub">Distance between where you land and the centre of the circles, in mm.</p><div class="chart" id="pOff"></div></div>
      </div>
      <div class="grid-2 section">
        <div class="panel"><h3>Getting used to the current area</h3><p class="sub">${ad.stable ? "You're used to the area: the measures are steady." : `In progress: ${nf(ad.hits, 0)} notes since you set it.`}</p><div class="chart" id="pAdapt"></div></div>
        <div class="panel"><h3>Warm-up</h3><p class="sub">Average error by minutes since the start of the session.</p><div class="chart" id="pWarm"></div></div>
      </div>
    </div>`;
    const base = { x: xs, height: 230, xFmt: dfmt, tipX: (x) => dateTime.format(new Date(x)), vmarks: vm, shade: bands, ml: 46, xTicks: undefined };
    const roll = (arr, k = 5) => arr.map((_, i) => {
      const win = arr.slice(Math.max(0, i - Math.floor(k / 2)), i + Math.ceil(k / 2)).filter((v) => v != null && isFinite(v));
      return win.length ? win.reduce((a, b) => a + b, 0) / win.length : null;
    });
    const errs = ses.map((s) => s.normErr), misses = ses.map((s) => s.missPer1k);
    Charts.draw($("#pErr"), Charts.line, { ...base, yFmt: (v) => nf(v * 100, 0) + "%", tipFmt: (v) => nf(v * 100, 1) + "%", series: [{ name: "session", values: errs, color: "var(--c1)", dots: true, dotR: 2.5, width: 1, dash: false }, { name: "trend", values: roll(errs), color: "var(--text)", width: 2.2 }] });
    Charts.draw($("#pMiss"), Charts.line, { ...base, yMin: 0, yFmt: (v) => nf(v, 0), tipFmt: (v) => nf(v, 1), series: [{ name: "session", values: misses, color: "var(--c2)", dots: true, dotR: 2.5, width: 1 }, { name: "trend", values: roll(misses), color: "var(--text)", width: 2.2 }] });
    const withBias = ses.map((s) => (s.hasBias ? s : null));
    Charts.draw($("#pGain"), Charts.line, { ...base, zero: true, yFmt: (v) => nf(v, 0) + "%", tipFmt: (v) => signed(v, 1) + "%", series: [{ name: "long/short", values: withBias.map((s) => (s ? s.gain : null)), color: "var(--c3)", dots: true }] });
    Charts.draw($("#pOff"), Charts.line, { ...base, yMin: 0, yFmt: (v) => nf(v, 1), tipFmt: (v) => nf(v, 2) + " mm", series: [{ name: "offset", values: withBias.map((s) => (s ? Math.hypot(s.offsetX, s.offsetY) : null)), color: "var(--c4)", dots: true }] });
    const ch = ad.chunks || [];
    if (ch.length >= 2) Charts.draw($("#pAdapt"), Charts.line, { x: ch.map((c) => c[0]), height: 220, ml: 46, xLabel: "notes played with this area", yFmt: (v) => nf(v * 100, 1) + "%", tipFmt: (v) => nf(v * 100, 1) + "%", tipX: (x) => `${nf(x, 0)} notes`, series: [{ name: "average error", values: ch.map((c) => c[1]), color: "var(--now)", dots: true }] });
    else $("#pAdapt").innerHTML = `<p class="muted small">At least 800 notes with the current area are needed.</p>`;
    const wu = p.warmup || [];
    if (wu.length >= 2) Charts.draw($("#pWarm"), Charts.line, { x: wu.map((w) => w[0]), height: 220, ml: 46, xLabel: "minutes since the start", xFmt: (v) => nf(v, 0), yFmt: (v) => nf(v * 100, 1) + "%", tipFmt: (v) => nf(v * 100, 1) + "%", tipX: (x) => `from minute ${nf(x, 0)}`, series: [{ name: "average error", values: wu.map((w) => w[1]), color: "var(--c1)", dots: true }] });
    else $("#pWarm").innerHTML = `<p class="muted small">Longer sessions are needed to see the effect of warming up.</p>`;
  }

  // Summary of every area used: how each one went, and the fairest
  // comparison with the current one (same maps played with both).
  function journey(r) {
    const eps = ((r.consistency && r.consistency.epochs) || []).slice().sort((a, b) => +d(a.from) - +d(b.from));
    if (!eps.length) return "";
    const cur = eps.find((e) => e.current);
    const known = eps.filter((e) => !e.unknown && !e.current);
    const prev = known.length ? known[known.length - 1] : null;
    // same size in different places: add the position to tell them apart
    const sizeKey = (e) => `${nfTrim(e.area.w, 1)}x${nfTrim(e.area.h, 1)}`;
    const dup = (e) => !e.unknown && eps.some((o) => o !== e && !o.unknown && sizeKey(o) === sizeKey(e) && (Math.abs(o.area.x - e.area.x) > 0.3 || Math.abs(o.area.y - e.area.y) > 0.3));
    const name = (e) => (e.unknown ? "Unknown area" : `${nfTrim(e.area.w, 1)} × ${nfTrim(e.area.h, 1)} mm${dup(e) ? ` <span class="muted xs">X ${nfTrim(e.area.x, 1)} · Y ${nfTrim(e.area.y, 1)}</span>` : ""}`);
    const err = (e) => e.byCat[5].normErr;
    let lead = "";
    if (cur && prev) {
      const ce = err(cur), pe = err(prev);
      const ch = ce / pe - 1;
      const few = cur.hits < 1500;
      lead = `<p class="bigline">From <b class="readout">${nfTrim(prev.area.w, 1)} mm</b> to <b class="readout">${nfTrim(cur.area.w, 1)} mm</b>: average error from ${pct(pe, 0)} to ${pct(ce, 0)} of the radius (<span class="${ch < -0.02 ? "delta-good" : ch > 0.02 ? "delta-bad" : ""}">${ch >= 0 ? "+" : "−"}${nf(Math.abs(ch) * 100, 0)}%</span>), aim misses from ${nf(prev.byCat[5].missPer1k, 1)} to ${nf(cur.byCat[5].missPer1k, 1)} per 1000 notes.</p>`;
      const sm = prev.sameMaps;
      if (sm) lead += `<p class="lede">Fairer comparison, on the ${sm.maps} maps played with both areas: with the current area you miss ${nf(Math.abs(sm.change) * 100, 0)}% ${sm.change < 0 ? "less" : "more"} (range ${sm.lo >= 0 ? "+" : "−"}${nf(Math.abs(sm.lo) * 100, 0)}% / ${sm.hi >= 0 ? "+" : "−"}${nf(Math.abs(sm.hi) * 100, 0)}%).${sm.lo < 0 && sm.hi > 0 ? " The difference isn't sure yet." : ""}</p>`;
      if (few) lead += `<p class="lede">You still have only ${nf(cur.hits, 0)} notes with the current area: right after a change it's normal to do a bit worse, the comparison gets reliable once you've adapted.</p>`;
    } else if (cur) {
      lead = `<p class="lede">So far I have data for one area only. When you change it, here you'll see the before and after.</p>`;
    }
    const row = (e) => {
      const sm = e.sameMaps;
      const cmp = e.current ? '<span class="tag cur">current</span>' : sm ? `<span class="${sm.hi < 0 ? "delta-good" : sm.lo > 0 ? "delta-bad" : ""}">${sm.change >= 0 ? "+" : "−"}${nf(Math.abs(sm.change) * 100, 0)}%</span> <span class="muted xs">on ${sm.maps} maps</span>` : '<span class="muted">–</span>';
      return `<tr><td><b style="font-weight:600">${name(e)}</b></td><td class="muted">${dateFmt.format(d(e.from))}${e.to ? " – " + dateFmt.format(d(e.to)) : e.current ? " – today" : ""}</td><td class="num">${nf(e.sessions, 0)}</td><td class="num">${nf(e.hits, 0)}</td><td class="num">${pct(err(e), 0)}</td><td class="num">${nf(e.byCat[5].missPer1k, 1)}</td><td class="num">${pct(e.byCat[5].inside, 1)}</td><td class="num">${cmp}</td></tr>`;
    };
    // newest first; with many areas the older ones are folded away
    const ordered = eps.slice().reverse();
    const rows = ordered.slice(0, 8).map(row).join("");
    const older = ordered.slice(8);
    const jHead = `<thead><tr><th>Area</th><th>Period</th><th class="num">Sessions</th><th class="num">Notes</th><th class="num">Average error</th><th class="num">Misses per 1000</th><th class="num">Inside the circle</th><th class="num">Current vs this</th></tr></thead>`;
    return `<section class="section journey">${sectionHead("Your path through the areas", "")}${lead}
      <div class="table-scroll"><table class="data" style="margin-top:12px">${jHead}<tbody>${rows}</tbody></table></div>
      ${older.length ? `<details class="why" style="margin-top:6px"><summary>Show the other ${older.length} areas</summary><div class="table-scroll"><table class="data">${jHead}<tbody>${older.map(row).join("")}</tbody></table></div></details>` : ""}
      <p class="xs muted" style="margin-top:8px">"Current vs this" compares the error of the current area with that of each earlier area on the same maps: negative values mean you miss less now.</p></section>`;
  }

  /* ----- Plays ----- */
  async function loadPlays(reset) {
    if (reset) state.plays.offset = 0;
    const q = encodeURIComponent(state.plays.q || "");
    const res = await api(`plays?q=${q}&offset=${state.plays.offset}&limit=100`);
    state.plays.total = res.total;
    state.plays.rows = reset ? res.rows : state.plays.rows.concat(res.rows);
  }
  function pagePlays(view) {
    const epochs = state.status?.epochs || [];
    const areaOf = (id) => epochs.find((e) => e.id === id);
    const rows = state.plays.rows || [];
    const profs = state.status?.profiles || [];
    const multi = profs.length > 1;
    const activeId = (profs.find((x) => x.active) || {}).id;
    view.innerHTML = `<div class="page">
      <div class="row" style="justify-content:space-between">
        <div><h1 class="h-page">Plays analysed</h1><p class="lede">${nf(state.plays.total, 0)} ${state.plays.total === 1 ? "replay" : "replays"}. Only replays saved since you first started Area Coach${state.status?.firstRun ? ` (${dateFmt.format(d(state.status.firstRun))})` : ""} are used. Click a row to see where you clicked. Each play goes by itself to the grip that was active when you played it; use the menu on the right to move it to another.${multi ? " The analysis only uses the plays of the active grip." : ""}</p></div>
        <input class="input" style="max-width:280px" type="search" id="playSearch" placeholder="Search map, mapper or mods" value="${esc(state.plays.q)}">
      </div>
      <div class="table-scroll"><table class="data" style="margin-top:18px"><thead><tr><th>Date</th><th>Map</th><th>Mods</th><th class="num">Acc.</th><th class="num">Average error</th><th class="num">Aim misses</th><th class="num">Area</th><th>Grip</th></tr></thead>
      <tbody>${rows.map((p) => {
        const e = areaOf(p.epoch);
        return `<tr class="click" data-id="${p.id}"><td class="muted">${dateTime.format(d(p.time))}</td><td><b style="font-weight:600">${esc(mapName(p))}</b>${p.version ? ` <span class="muted">[${esc(p.version)}]</span>` : ""}${p.calibration ? ' <span class="tag">test</span>' : ""}</td><td>${p.modStr ? `<span class="tag mods">${esc(p.modStr)}</span>` : ""}</td><td class="num">${pct(p.accuracy, 2)}</td><td class="num">${pct(p.normErr, 0)}</td><td class="num">${nf(p.summary.aimMiss, 0)}</td><td class="num">${e ? `${nfTrim(e.setup.area.w, 1)} mm` : '<span class="muted">unknown</span>'}</td><td class="grip-cell"><select class="grip-sel ${p.profile === activeId ? "" : "other"}" data-pprof="${p.id}" aria-label="Grip of the play">${profs.map((x) => `<option value="${x.id}" ${x.id === p.profile ? "selected" : ""}>${esc(x.name)}</option>`).join("")}</select></td></tr>`;
      }).join("")}</tbody></table></div>
      ${rows.length < state.plays.total ? `<div style="margin-top:14px"><button class="btn" id="morePlays">Show more</button></div>` : ""}
      ${!rows.length ? needsData("No replays yet", "Play a map and finish it: osu! saves the replay and the program analyses it automatically. Replays from before you started Area Coach aren't used.") : ""}
      ${skippedSection()}
      <section class="section">${sectionHead("Which replays I pick up", "Only the ones saved since you first started Area Coach: older replays are ignored. osu! saves a replay by itself only when you finish a map playing solo (it goes into the hidden Data/r folder). In multiplayer it never saves one, and when you fail it doesn't either: in those cases press F2 on the results or game-over screen, osu! puts it in the Replays folder and I pick it up from there. If you quit halfway with Esc there is no replay. I don't use replays with Relax, Autopilot, Auto, Cinema or Target, or replays of maps that aren't osu!standard.")}${foldersBox()}</section>
    </div>`;
    const s = $("#playSearch");
    s.addEventListener("input", () => {
      clearTimeout(s._t);
      s._t = setTimeout(async () => { state.plays.q = s.value; await loadPlays(true); render(); $("#playSearch").focus(); }, 300);
    });
    const more = $("#morePlays");
    if (more) more.addEventListener("click", async () => { state.plays.offset += 100; await loadPlays(false); render(); });
    $$("tr.click", view).forEach((tr) => tr.addEventListener("click", (ev) => { if (!ev.target.closest(".grip-cell")) openPlay(+tr.dataset.id); }));
    $$("[data-pprof]", view).forEach((sel) => sel.addEventListener("change", async () => {
      const id = +sel.dataset.pprof, prof = +sel.value;
      try {
        await api("profile", { action: "assign", play: id, profile: prof });
        const row = state.plays.rows.find((r) => r.id === id);
        if (row) { row.profile = prof; row.profileManual = true; }
        sel.classList.toggle("other", prof !== activeId);
        toast(`Play moved to "${profName(prof)}"`);
        refreshStatus();
      } catch (e) { toast(e.message); }
    }));
  }

  // the folders looked at for replays, to check that osu! saves them there
  function foldersBox() {
    const fs = (state.status && state.status.import && state.status.import.folders) || [];
    if (!fs.length) return "";
    const li = fs.map((f) => `<li><span class="readout small">${esc(f.path)}</span>: ${!f.exists ? "<b>doesn't exist</b>" : f.files ? `${nf(f.files, 0)} replays, the last one saved ${dateTime.format(d(f.latest))}` : "empty"}</li>`).join("");
    return `<div class="panel"><h3>Where I look for them</h3><ul class="insights" style="margin-top:8px">${li}</ul>
      <p class="xs muted" style="margin-top:8px">If you finish a map solo and the time of the last replay in Data\\r here doesn't change, osu! isn't saving there (or I'm looking at the folder of another osu! install: pick the right one in Settings).</p></div>`;
  }
  // the replays found and not used, and why
  function skippedSection() {
    const sk = (state.status && state.status.import && state.status.import.skipped) || [];
    if (!sk.length) return "";
    const rows = sk.map((x) => `<tr><td class="muted">${dateTime.format(d(x.when))}</td><td><b style="font-weight:600">${esc(x.map || "unknown map")}</b><div class="xs muted">${esc(x.file)}</div></td><td class="small">${esc(x.reason)}${x.retry ? ' <span class="tag">will retry</span>' : ""}</td></tr>`).join("");
    return `<section class="section">${sectionHead("Replays found but not used", "The replays I found in the osu! folders and didn't analyse, with the reason. The last 40, newest first.")}
      <div class="table-scroll"><table class="data"><thead><tr><th>Saved</th><th>Map</th><th>Why I don't use it</th></tr></thead><tbody>${rows}</tbody></table></div></section>`;
  }

  async function openPlay(id) {
    const res = await api("play/" + id);
    const p = res.play;
    const bg = document.createElement("div");
    bg.className = "drawer-bg";
    const dr = document.createElement("aside");
    dr.className = "drawer";
    dr.setAttribute("role", "dialog");
    dr.setAttribute("aria-label", "Play details");
    const hits = res.hits;
    const clicks = hits.filter((h) => h.ex != null);
    const inside = clicks.filter((h) => Math.hypot(h.ex, h.ey) <= h.ra).length;
    const aimMiss = hits.filter((h) => h.r === 3).length;
    const noPress = hits.filter((h) => h.r === 4).length;
    dr.innerHTML = `<button class="btn small ghost close" aria-label="Close">Close</button>
      <h2 class="h-page" style="padding-right:80px">${esc(mapName(p))}</h2>
      <p class="muted">${p.version ? `[${esc(p.version)}] ` : ""}${p.creator ? `by ${esc(p.creator)} · ` : ""}${dateTime.format(d(p.time))}${p.modStr ? " · " + esc(p.modStr) : ""}</p>
      <div class="stats" style="margin-top:14px">
        <div class="stat"><div class="k">Accuracy</div><div class="v readout">${pct(p.accuracy, 2)}</div><div class="h">${p.nMiss} misses in total</div></div>
        <div class="stat"><div class="k">Clicks inside the circle</div><div class="v readout">${pct(clicks.length ? inside / clicks.length : null, 1)}</div><div class="h">${clicks.length} clicks</div></div>
        <div class="stat"><div class="k">Aim misses</div><div class="v readout">${aimMiss}</div><div class="h">outside the circle</div></div>
        <div class="stat"><div class="k">Notes not pressed</div><div class="v readout">${noPress}</div><div class="h">rhythm or reading</div></div>
      </div>
      <section class="section">${sectionHead("Where you clicked", "Each segment goes from the centre of the note to the click, magnified 3 times. Clicks outside the circle are in red.")}<div class="chart" id="pfChart"></div></section>
      <p class="xs muted">CS ${nf(p.cs, 1)} · AR ${nf(p.ar, 1)} · OD ${nf(p.od, 1)} · ${nf(p.bpm, 0)} BPM · ${res.area ? `area ${nfTrim(res.area.w, 1)} × ${nfTrim(res.area.h, 1)} mm` : "unknown area"}</p>
      <div class="field" style="margin-top:14px;max-width:360px"><label for="playProf">Grip of this play</label><select class="input" id="playProf"><option value="0" ${res.profileManual ? "" : "selected"}>Automatic, by date (${esc(profName(res.profile))})</option>${(state.status?.profiles || []).map((x) => `<option value="${x.id}" ${res.profileManual && res.profile === x.id ? "selected" : ""}>${esc(x.name)}</option>`).join("")}</select></div>`;
    document.body.append(bg, dr);
    const close = () => { bg.remove(); dr.remove(); document.removeEventListener("keydown", onKey); };
    const onKey = (e) => { if (e.key === "Escape") close(); };
    document.addEventListener("keydown", onKey);
    bg.addEventListener("click", close);
    $(".close", dr).addEventListener("click", close);
    $(".close", dr).focus();
    Charts.draw($("#pfChart", dr), Charts.playfield, { hits, mag: 3, height: 460 });
    $("#playProf", dr).addEventListener("change", async (ev) => {
      try {
        await api("profile", { action: "assign", play: id, profile: +ev.target.value });
        toast("Grip of the play updated: recalculating");
        await refreshStatus();
        await loadPlays(true);
        if (state.route === "plays") { const keep = $("#view").scrollTop; render(); $("#view").scrollTop = keep; }
      } catch (e) { toast(e.message); }
    });
  }

  /* ----- Diagnosis helpers ----- */
  const areaTxt = (r) => `${nfTrim(r.w, 1)} × ${nfTrim(r.h, 1)} mm`;

  async function calibDo(action, msg) {
    try {
      await api("calib", { action });
      if (msg) toast(msg);
      await refreshStatus();
      render();
    } catch (e) { toast(e.message); }
  }

  /* ----- Diagnosis: one area a map, the next one from its replay ----- */
  const mmTxt = (v) => { const a = Math.abs(v); return a >= 1 ? nf(v, 1) : a >= 0.1 ? nf(v, 2) : nf(v, 3); };
  const rectTxt = (r) => `${nfTrim(r.w, 1)} × ${nfTrim(r.h, 1)} mm`;
  const posTxt = (r) => `X ${nfTrim(r.x, 1)} · Y ${nfTrim(r.y, 1)}`;
  const DIAG_TIPS = `<ul class="insights" style="margin-top:12px">
      <li><b>Play each map from start to finish with the same area.</b> When you pass a map playing solo, osu! saves the replay by itself: that's what I measure everything from. <b>In multiplayer osu! never saves it</b>: press F2 on the results screen. <b>If you fail, press F2 on the game-over screen</b>: osu! saves the replay of the part you played in the Replays folder, and I use it up to where you got. With NoFail you never fail and don't need to press anything. If you quit halfway with Esc there is no replay, and that map doesn't count.</li>
      <li><b>Varied maps</b>: jumps, streams, high AR, small and big circles. Your usual ones or a bit harder. The more jumps there are, the sooner I know whether you land long or short.</li>
      <li><b>Play normally</b>, without correcting yourself: the area adapts to you. Even if at first, with the whole tablet, you hit almost nothing: from how you move the pen I can tell how short you land.</li>
      <li><b>Every map has a slightly different area</b>: the best found so far, a bit bigger or smaller in turn and by varying amounts. It isn't a mistake: that's how I find the width you're most precise and steady with. The differences shrink as the perfection percentage goes up.</li>
      <li><b>At the end of the map</b>, as soon as osu! saves the replay (you're on the results screen), I work out the area for the next map and put it in OpenTabletDriver, saved. It takes a few seconds: before starting the next map, check that the new area has arrived here.</li>
      <li><b>What it uses.</b> Only the replays osu! saves to disk, as in the normal analysis. The area in OpenTabletDriver is changed only between maps, as you would by hand.</li>
      <li><b>The OpenTabletDriver window</b> can stay open: it keeps showing the area numbers from when you opened it, and that's fine. If you press Apply or Save there, I put the right area back by myself.</li>
    </ul>`;
  const sidePair = (dx, dy) => {
    const parts = [];
    if (Math.abs(dx) >= 0.05) parts.push(`${mmTxt(Math.abs(dx))} mm ${dx > 0 ? "right" : "left"}`);
    if (Math.abs(dy) >= 0.05) parts.push(`${mmTxt(Math.abs(dy))} mm ${dy > 0 ? "low" : "high"}`);
    return parts;
  };
  const gainTxt2 = (g) => {
    const p = (Math.exp(g) - 1) * 100;
    if (Math.abs(p) < 0.5) return "spot on";
    return p > 0 ? `long (+${nf(p, 1)}%)` : `short (−${nf(-p, 1)}%)`;
  };
  // what a map measured, in words
  function mapWords(m) {
    const parts = [`on jumps you land ${gainTxt2(m.g || 0)}`];
    const off = sidePair(...userVec(m.dx || 0, m.dy || 0, m.area && m.area.rot));
    parts.push(off.length ? `clicks ${off.join(" and ")} of the circles` : "clicks centred on the circles");
    if (Math.abs(m.rot || 0) >= 0.3) parts.push(`jumps turned ${nf(Math.abs(m.rot), 1)}° ${m.rot > 0 ? "clockwise" : "anticlockwise"}`);
    parts.push(m.aimMiss ? `${m.aimMiss} aim ${m.aimMiss === 1 ? "miss" : "misses"}` : "no aim misses");
    return parts.join("; ");
  }
  // what a change did, in words
  function changeWords(from, to, locked) {
    if (!from || !to || !from.w) return "";
    const dw = to.w - from.w, dh = to.h - from.h;
    const parts = [];
    if (locked) {
      if (Math.abs(dw) >= 0.0005) parts.push(`${mmTxt(Math.abs(dw))} mm ${dw > 0 ? "bigger" : "smaller"}`);
    } else {
      if (Math.abs(dw) >= 0.0005) parts.push(`${mmTxt(Math.abs(dw))} mm ${dw > 0 ? "wider" : "narrower"}`);
      if (Math.abs(dh) >= 0.0005) parts.push(`${mmTxt(Math.abs(dh))} mm ${dh > 0 ? "taller" : "shorter"}`);
    }
    const mv = [];
    const [dx, dy] = userVec(to.x - from.x, to.y - from.y, to.rot);
    if (Math.abs(dx) >= 0.0005) mv.push(`${mmTxt(Math.abs(dx))} mm ${dx > 0 ? "right" : "left"}`);
    if (Math.abs(dy) >= 0.0005) mv.push(`${mmTxt(Math.abs(dy))} mm ${dy > 0 ? "down" : "up"}`);
    if (mv.length) parts.push(`moved ${mv.join(" and ")}`);
    return parts.length ? `area ${parts.join(", ")}` : "area unchanged";
  }
  const rectMove = (x, y) => Math.max(Math.abs(y.w - x.w) / 2 + Math.abs(y.x - x.x), Math.abs(y.h - x.h) / 2 + Math.abs(y.y - x.y));
  // how much the best area still changes from one map to the next
  function steadyInfo(a) {
    // the best area found after each map (not the one played: that one
    // differs from it on purpose)
    const ms = (a.maps || []).filter((m) => m.windows > 0 && m.best && m.best.w);
    const mv = [];
    for (let i = 1; i < ms.length; i++) mv.push(rectMove(ms[i - 1].best, ms[i].best));
    if (mv.length < 2) return { k: "Too early to say", t: "a couple more maps are needed" };
    const s = (mv[mv.length - 1] + mv[mv.length - 2]) / 2;
    if (s >= 2) return { k: "Searching", t: `the best area still changes by about ${mmTxt(s)} mm per map` };
    if (s >= 0.5) return { k: "Getting closer", t: `the best area changes by about ${mmTxt(s)} mm per map` };
    if (s >= 0.1) return { k: "Almost found", t: `the best area changes by a few tenths (${mmTxt(s)} mm)` };
    return { k: "Found", t: `the best area now changes by ${mmTxt(s)} mm per map: if it feels comfortable, it's yours` };
  }
  // how close the area found is to a perfect one
  function perfBlock(a) {
    const sc = (a.fit && a.fit.score) || {};
    const p = sc.pct || 0;
    const lines = sc.lines > 0 ? ` · lines ${nf(sc.lines * 100, 1)}% curved` : "";
    return `<div class="perf" title="How close the area found is to a perfect one: one the diagnosis is sure about, with which the aim misses nothing and goes straight from one circle to the next. 100% can't be reached: missing is human.">
        <div class="perf-head"><span>Area perfection</span><b class="readout">${a.n ? `${nf(p, 1)}%` : "–"}</b></div>
        <div class="perf-bar"><span style="width:${Math.max(0, Math.min(100, p))}%"></span></div>
        <div class="xs muted" style="margin-top:4px">${a.n ? `Certainty ${nf(sc.sure || 0, 0)}% · clean aim ${nf(sc.clean || 0, 0)}% (${nf(sc.miss || 0, 1)} expected aim misses per 1000 notes${lines})` : "comes with the first map"}</div>
      </div>`;
  }
  // where on the tablet the hand is steadier
  function steadyLine(a) {
    const s = (a.fit && a.fit.steady) || {};
    if (a.posFixed) return `<span class="muted small">position held: I don't try other places</span>`;
    if (!(s.n >= 8)) return `<b>Measuring it</b><span class="muted small">every two maps I try the area a little right, left, below or above the best place and look at how much the clicks spread</span>`;
    const part = (g, se, sh, plus, minus) => {
      if (!(se > 0) || Math.abs(g) < se) return "";
      const pct = (Math.exp((g * 5) / 2) - 1) * 100;
      let t = `5 mm further ${g > 0 ? plus : minus} the clicks spread ${nf(Math.abs(pct), 0)}% more`;
      if (Math.abs(sh) >= 0.1) t += ` (that's why I moved it ${nfTrim(Math.abs(sh), 1)} mm ${sh < 0 ? minus : plus})`;
      return t;
    };
    // as seen on the screen: the area may be rotated
    const rot = ((((a.cur && a.cur.rot) || 0) * Math.PI) / 180), rc = Math.abs(Math.cos(rot)), rs = Math.abs(Math.sin(rot));
    const [gx, gy] = userVec(s.gx || 0, s.gy || 0, a.cur && a.cur.rot);
    const [shx, shy] = userVec(s.shiftX || 0, s.shiftY || 0, a.cur && a.cur.rot);
    const gxse = Math.hypot(rc * (s.gxse || 0), rs * (s.gyse || 0)), gyse = Math.hypot(rs * (s.gxse || 0), rc * (s.gyse || 0));
    const ps = [part(gx, gxse, shx, "right", "left"), part(gy, gyse, shy, "down", "up")].filter(Boolean);
    if (!ps.length) return `<b>No clear difference</b><span class="muted small">around the place found you're just as steady a little right, left, above or below: the position is decided by where the clicks land</span>`;
    return `<b>${ps.length === 2 ? "Two sides" : "One side is better"}</b><span class="muted small">${esc(ps.join("; "))}. If moving the area doesn't change where your clicks land against the circles (your wrist follows it), where you're steadier matters more.</span>`;
  }
  // turned aim: how much, where the wrist is, where the aim comes out straight
  function arcInfo(a) {
    const r = (a.fit && a.fit.arc) || {};
    const otdRot = `I never change the OpenTabletDriver rotation (${nfTrim((a.cur && a.cur.rot) || 0, 1)}°): if needed I move the area.`;
    if (!(r.n >= 3)) return { k: "Measuring it", t: `From the first maps I look at whether the jumps come out turned and how that changes from one side of the tablet to the other. ${otdRot}` };
    const m = r.mean || 0;
    const k = Math.abs(m) < 0.3 ? "Straight aim" : `Turned ${nf(Math.abs(m), 1)}° ${m > 0 ? "clockwise" : "anticlockwise"}`;
    let t = "";
    if (r.strong) {
      const g = Math.hypot(r.gx || 0, r.gy || 0);
      // the side of the pivot as seen on the screen (the area may be rotated)
      const dir = ((((r.dir || 0) - ((a.cur && a.cur.rot) || 0)) % 360) + 540) % 360 - 180;
      const side = Math.abs(dir) < 10 ? "the pivot (wrist or elbow) is right below the area" : dir < 0 ? "the wrist is below and to the right of the area" : "the wrist is below and to the left of the area";
      t += `From left to right the rotation changes by ${nf(g, 2)}° every 10 mm: ${side}. `;
    }
    if (a.posFixed) t += "With the position held I don't move the area for the rotation. ";
    else {
      if (Math.abs(r.zero || 0) >= 1 && Math.abs(m) >= 0.3) t += `The aim comes out straight about ${mmTxt(Math.abs(r.zero))} mm further ${r.zero > 0 ? "right" : "left"}. `;
      if (Math.abs(r.shift || 0) >= 0.3) t += `For the next map I move the area ${mmTxt(Math.abs(r.shift))} mm ${r.shift > 0 ? "right" : "left"} (at most 6 mm per map) and measure again. `;
      if (r.note) t += r.note.charAt(0).toUpperCase() + r.note.slice(1) + ". ";
    }
    return { k, t: t + otdRot };
  }
  // the widths the player allows, as typed
  const rangeTxtOf = (set) => !set || !(set.diagMinW > 0) ? "" : set.diagMaxW > 0 ? `${nfTrim(set.diagMinW, 1)} - ${nfTrim(set.diagMaxW, 1)}` : `${nfTrim(set.diagMinW, 1)} -`;
  const rangeField = (id) => {
    const set = (state.status && state.status.settings) || {};
    return `<div class="field"><label for="${id}">Widths allowed (mm)</label><input class="input" id="${id}" value="${esc(rangeTxtOf(set))}" placeholder="for example 110 - 150" inputmode="decimal" style="width:170px"></div>`;
  };
  const rangeWords = () => {
    const set = (state.status && state.status.settings) || {};
    if (!(set.diagMinW > 0)) return "No limit: the diagnosis can use any width that fits the tablet. Type for example 110 - 150 to never get areas narrower than 110 mm or wider than 150 mm; a single number is the minimum.";
    return set.diagMaxW > 0 ? `Never areas narrower than ${nfTrim(set.diagMinW, 1)} mm or wider than ${nfTrim(set.diagMaxW, 1)} mm. Clear the field to remove the limit.` : `Never areas narrower than ${nfTrim(set.diagMinW, 1)} mm. Clear the field to remove the limit.`;
  };
  async function saveRange(el) {
    try {
      await api("settings", { diagRange: el.value.trim() });
      const set = (state.status && state.status.settings) || {};
      await refreshStatus();
      const ns = (state.status && state.status.settings) || {};
      toast(ns.diagMinW > 0 ? `Widths allowed: ${rangeTxtOf(ns).replace(/ -$/, " mm or more")}${ns.diagMaxW > 0 ? " mm" : ""}` : set.diagMinW > 0 ? "Width limit removed" : "No width limit");
      render();
    } catch (e) { toast(e.message); }
  }
  // where a new diagnosis starts from
  const FROMS = [["full", "Whole tablet"], ["current", "Current area"], ["custom", "Pick the width"]];
  const fromSeg = (from, attr, list) => `<div class="seg" role="group" aria-label="Area to start from">${(list || FROMS).map(([k, l]) => `<button ${attr}="${k}" class="${from === k ? "on" : ""}" aria-pressed="${from === k}">${l}</button>`).join("")}</div>`;

  // the Radial Follow that goes with the area found, in one line
  const rfShort = (p) => `outer ${nfTrim(p.outer, 2)} · inner ${nfTrim(p.inner, 2)} · coef ${nfTrim(p.coef, 2)} · knee ${nfTrim(p.knee, 2)} · leak ${nfTrim(p.leak, 2)}`;
  function filterLine(a) {
    const fc = a.coach;
    if (fc && fc.active) {
      if (!fc.askOn) return `<b>Next map without a filter</b><span class="muted small">${fc.askRef ? "to compare: then I go back to the values" : "you do better without a filter"} · see The filter, below</span>`;
      return `<b>Radial Follow${fc.lowLag ? " (lowest latency)" : ""}</b><span class="muted small readout">${rfShort(fc.ask)}${fc.isSet ? " · already set" : " · to set in OpenTabletDriver"}</span>`;
    }
    if (fc && !fc.active) return `<span class="muted small">guided filter stopped · best found: ${fc.bestOn ? esc(rfShort(fc.best)) : "no filter"}</span>`;
    return `<span class="muted small">not guided: see The filter, below</span>`;
  }
  // what the cursor did with a filter, in words
  const tremTxt = (t) => t == null || isNaN(t) ? "–" : `${nf(t * 100, t < 0.1 ? 1 : 0)}% of the radius`;
  function coachPerf(fc) {
    const p = fc.pct || 0;
    return `<div class="perf" title="How close the filter found is to a perfect one: one I'm sure about (where the best is, and that it really helps compared with no filter) and with which the aim is clean: few aim misses, the cursor still on the circle, arriving in time for the click. 100% can't be reached.">
        <div class="perf-head"><span>Filter perfection</span><b class="readout">${fc.nOn ? `${nf(p, 1)}%` : "–"}</b></div>
        <div class="perf-bar"><span style="width:${Math.max(0, Math.min(100, p))}%"></span></div>
        <div class="xs muted" style="margin-top:4px">${fc.nOn ? `Certainty ${nf(fc.sure || 0, 0)}% · clean aim ${nf(fc.clean || 0, 0)}% (aim misses ${nf(fc.aim || 0, 0)}%, cursor still ${nf(fc.still || 0, 0)}%, on time ${nf(fc.time || 0, 0)}%)` : "comes with the first map played with the filter"}</div>
      </div>`;
  }
  function diagFilter(a) {
    const fc = a.coach;
    const now = fc && fc.nowKnown ? fc : null;
    const missingPlugin = !a.filterHas
      ? `<div class="note-bar" style="margin-top:12px">${ICON.info}<span>I can't find the plugin in OpenTabletDriver. Install <b>AbstractQbit's Radial Follow Smoothing</b> from Plugins → Plugin Manager, then in Filters add <b>AbstractQbit's Radial Follow Smoothing (Tablet coordinates)</b>.</span></div>`
      : "";
    const head = "The filter (Radial Follow)";
    const intro = "AbstractQbit's Radial Follow, a smoothing filter for OpenTabletDriver: it reduces pen jitter; the stronger it is, the more delay it adds. I don't change the filter myself: I tell you the values, you put them in OpenTabletDriver, and after every replay I give you new ones. As with the area, every map tries values a little stronger or lighter than the best, and all of them help to find it.";
    if (!fc || !fc.active) {
      const last = fc ? `<p class="small" style="margin-top:8px">The last best found: <b class="readout">${fc.bestOn ? esc(rfShort(fc.best)) : "no filter"}</b>${fc.nOn ? ` · perfection ${nf(fc.pct || 0, 1)}%` : ""}.</p>` : "";
      const sim = a.hasFamily ? "The simulation on your movements is ready: I start from there." : a.filterBusy ? "I'm simulating the filter on your movements: if you start now I begin from average values and correct them with the maps." : `I start from average values; after about ${nf(a.filterNeed || 600, 0)} clicks without a filter with an area close to the one found, I also simulate them on your movements.`;
      const body = `<h3>${fc ? "Guided filter stopped" : "Guided filter"}</h3>
        <p class="sub">Press Start and I tell you the values for the next map. From the replay I measure how many circles you miss against how many you usually miss on those notes without a filter, how much the cursor shakes on the circle before the click, how many clicks come at the last moment and how straight the lines are. Now and then I ask for a map without a filter, to compare. ${sim}</p>
        ${last}
        <p class="small" style="margin-top:10px">How much delay is fine? With <b>lowest latency</b> I only suggest values with at most 3 ms of average delay and, for the same misses, the fastest: the cursor follows the pen almost at once and shakes a little more. With <b>more smoothing</b> I accept up to 15 ms if it helps: less jitter, a bit softer feel. You can change it any time.</p>
        <div class="row" style="margin-top:10px;gap:10px;flex-wrap:wrap;align-items:center"><button class="btn primary" data-fcstart="fc-start-low">${fc ? "Resume" : "Start"} with lowest latency</button><button class="btn" data-fcstart="fc-start">${fc ? "Resume" : "Start"} with more smoothing</button><a class="small" href="#/filter">The full filter study</a></div>
        ${missingPlugin}`;
      return `<section class="section">${sectionHead(head, intro)}<div class="panel">${body}</div></section>`;
    }
    // what to put for the next map
    const why = fc.askRef ? "A map without a filter, to compare: that's how I know how much it really helps."
      : !fc.askOn ? "With the values tried so far you do better without a filter. Now and then I try the best again, to see if that changes."
      : fc.hold ? "The best found so far (keep the best: no tries)."
      : fc.askTry > 0.01 ? `A little stronger than the best found (radii ${nf((Math.exp(fc.askTry) - 1) * 100, 0)}% bigger): every map tries a slightly different filter, and all of them help to find the best.`
      : fc.askTry < -0.01 ? `A little lighter than the best found (radii ${nf((1 - Math.exp(fc.askTry)) * 100, 0)}% smaller): every map tries a slightly different filter, and all of them help to find the best.`
      : "The best found so far: I measure it again, so I know how well it does.";
    const nowTxt = now ? (now.nowOn ? `In OpenTabletDriver now: ${rfShort(now.now)}${now.nowTablet ? "" : " (Screen coordinates version)"}` : "In OpenTabletDriver Radial Follow is off now") : "";
    let status;
    if (now) {
      status = fc.isSet ? `<span class="tag ok">OpenTabletDriver already has ${fc.askOn ? "these values" : "the filter off"}</span><span class="small muted">play the map</span>`
        : `<span class="tag cur">to set</span><span class="small muted">${esc(nowTxt)}. ${fc.askOn ? "Type in the values above and press Apply." : "Untick the filter and press Apply."}</span>`;
    } else {
      status = fc.confirmed ? `<span class="tag ok">set</span><span class="small muted">play the map</span>`
        : `<span class="small muted">I can't read the OpenTabletDriver filters. When you've set them press</span><button class="btn small" id="fcConfirm">I've set them</button>`;
    }
    const foot = `In OpenTabletDriver: Filters → <b>AbstractQbit's Radial Follow Smoothing (Tablet coordinates)</b>, tick Enable, type in the values and press <b>Apply</b> (Save to keep them after a restart too). If the window shows an old area it doesn't matter: after Apply I put the right one back, the filter stays.`;
    const px = fc.askPx && fc.askOn ? `<div class="note-bar" style="margin-top:10px">${ICON.info}<span>You have the <b>Screen coordinates</b> version: in pixels the radii are outer <b class="readout">${nfTrim(fc.askPx.outer, 1)}</b> and inner <b class="readout">${nfTrim(fc.askPx.inner, 1)}</b>, and they change with every area change. The Tablet coordinates version is better: the millimetres stay right whatever the area.</span></div>` : "";
    const ask = fc.askOn
      ? rfTable(fc.ask, now && now.nowOn && now.nowTablet ? now.now : null, foot)
      : `<div class="otd"><div class="otd-head"><span>AbstractQbit's Radial Follow Smoothing</span></div><div class="otd-foot" style="border-top:0"><b>Off</b> for the next map: in OpenTabletDriver, Filters, untick Enable on the filter and press Apply.</div></div>`;
    // the best one so far
    const eff = !fc.bestOn ? "without a filter" : fc.known && fc.nOn >= 2 && fc.nOff >= 1
      ? (fc.impHi - fc.impLo > 0.8 ? "still too few misses to say how much it helps" : `${fc.improve >= 0 ? `${nf(fc.improve * 100, 0)}% fewer aim misses` : `${nf(-fc.improve * 100, 0)}% more aim misses`} than without a filter (between ${signedPct(fc.impLo)} and ${signedPct(fc.impHi)})`)
      : "how much it helps compared with no filter I can say after a few maps with and without";
    const best = `<div class="kv-line"><span>Best so far</span><b class="readout">${fc.bestOn ? esc(rfShort(fc.best)) : "no filter"}</b><span class="muted small">${esc(eff)}${fc.note ? ` · ${esc(fc.note)}` : ""}</span></div>
      <div class="kv-line"><span>Cursor on the circle</span><b>${fc.bestOn && fc.nOn ? `shakes ${tremTxt(fc.trem || 0)}` : "–"}</b><span class="muted small">before the click, with the best${fc.nOff || fc.sim ? `; without a filter ${tremTxt(fc.tremOff || 0)}` : ""}. The lower, the stiller the cursor while you click.</span></div>
      ${fc.sim && fc.bestOn ? `<div class="kv-line"><span>Delay</span><b class="readout">${nf(fc.delay || 0, 1)} ms</b><span class="muted small">on average to reach the circle, simulated on your movements; ${nf(fc.lost || 0, 1)} clicks per 1000 would land outside because the cursor arrives late. I never suggest values with more than ${nf(fc.maxLag || 15, 0)} ms of delay.</span></div>` : ""}
      <div class="kv-line"><span>Maps measured</span><b class="readout">${fc.nOn} with a filter · ${fc.nOff} without</b><span class="muted small">${fc.sim ? (fc.simSrc === "replay" ? "together with the simulation on the movements of your replays without a filter" : "together with the simulation on the movements of the diagnosis maps without a filter") : "the simulation comes after a few maps without a filter"}</span></div>`;
    const lagCtl = `<div class="row" style="margin-top:14px;align-items:center;gap:10px;flex-wrap:wrap"><span class="small">Latency</span><div class="seg" role="group" aria-label="Filter latency"><button data-fc="fc-lowlag" class="${fc.lowLag ? "on" : ""}" aria-pressed="${!!fc.lowLag}">As low as possible</button><button data-fc="fc-normal" class="${fc.lowLag ? "" : "on"}" aria-pressed="${!fc.lowLag}">Some is fine (more smoothing)</button></div></div>
      <p class="xs muted" style="margin-top:4px">${fc.lowLag ? `Only values with at most ${nf(fc.maxLag || 3, 0)} ms of average delay and, for the same misses, the fastest: the cursor follows the pen almost at once.` : `Up to ${nf(fc.maxLag || 15, 0)} ms of average delay, if it helps: less jitter, a bit softer feel.`} If you switch, the values for the next map change right away.</p>`;
    const ctl = `${lagCtl}<div class="row" style="margin-top:14px;align-items:center;gap:10px;flex-wrap:wrap"><div class="seg" role="group" aria-label="Filter values"><button data-fc="fc-tries" class="${fc.hold ? "" : "on"}" aria-pressed="${!fc.hold}">Try different values</button><button data-fc="fc-hold" class="${fc.hold ? "on" : ""}" aria-pressed="${!!fc.hold}">Keep the best</button></div><button class="btn small ghost" id="fcStop">Stop the guided filter</button></div>`;
    // the maps, with the filter each was played with
    const ms = (a.maps || []).filter((m) => m.windows > 0).slice(-12).reverse();
    const fcell = (m) => m.filter === "on" && m.fparams ? `<span class="readout xs">${esc(rfShort(m.fparams))}</span>` : m.filter === "off" ? "none" : m.filter === "mixed" ? "changed during the map" : "–";
    const rows = ms.map((m) => `<tr><td>${esc(m.title)}</td><td>${fcell(m)}</td><td class="num">${m.aimMiss || 0}${m.exp > 0 ? ` <span class="muted xs">/ ${nf(m.exp, 1)}</span>` : ""}</td><td class="num">${m.curN > 0 ? tremTxt(m.trem || 0) : "–"}</td><td class="num">${m.curN > 0 ? `${nf((m.late || 0) * 100, 0)}%` : "–"}</td><td class="num">${m.line > 0 ? `${nf(m.line * 100, 1)}%` : "–"}</td></tr>`).join("");
    const table = ms.length ? `<div class="table-scroll" style="margin-top:14px"><table class="data ftest"><thead><tr><th>Map</th><th>Filter</th><th class="num">Aim misses</th><th class="num" title="How much the cursor moves back and forth on the circle right before the click">Shake</th><th class="num" title="Clicks pressed just as the cursor entered the circle, or outside it">Last moment</th><th class="num">Curves</th></tr></thead><tbody>${rows}</tbody></table></div>
      <p class="xs muted" style="margin-top:6px">Aim misses: the ones made, and how many you'd usually make on those notes with that area without a filter. Shake: how much the cursor moves back and forth on the circle in the last instant before the click, against the radius. Last moment: clicks pressed just as the cursor entered the circle (or outside it): too many means too much delay.</p>` : "";
    const body = `${coachPerf(fc)}
      <h3 style="margin-top:16px">For the next map</h3><p class="sub">${esc(why)}</p>
      ${ask}${px}
      <div class="row" style="margin-top:10px;align-items:center;gap:8px;flex-wrap:wrap">${status}</div>
      <div style="margin-top:14px">${best}</div>
      ${ctl}${missingPlugin}${table}`;
    return `<section class="section">${sectionHead(head, intro)}<div class="panel">${body}</div></section>`;
  }
  const signedPct = (v) => `${v >= 0 ? "−" : "+"}${nf(Math.abs(v) * 100, 0)}%`;
  // the shape of the area: of the screen (forced proportion), fixed by the
  // player, or free
  const SHAPES = [["screen", "Like the screen"], ["fixed", "Fixed"], ["free", "Free"]];
  const ratioVal = (txt) => { const v = parseFloat(String(txt || "").replace(",", ".")); return isFinite(v) ? v : NaN; };
  const shapeSeg = (shape, attr) => `<div class="seg" role="group" aria-label="Area shape">${SHAPES.map(([k, l]) => `<button ${attr}="${k}" class="${shape === k ? "on" : ""}" aria-pressed="${shape === k}">${l}</button>`).join("")}</div>`;
  const shapeWords = (shape, ratio, from, screen) => {
    if (shape === "screen") return `Forced proportions on: every area has the proportions of the screen (height/width ${nfTrim(screen, 4)}).`;
    if (shape === "fixed") return `Fixed shape: height/width ${nfTrim(ratio, 3)}, you decide. The diagnosis only changes size and position. For example 0.75 is 4:3, ${nfTrim(screen, 4)} is the screen.`;
    return `Forced proportions off: width and height adapt each on their own, but the shape stays close to the one it starts from${from ? ` (height/width ${nfTrim(from, 2)})` : ""} and only partly follows the difference between vertical and horizontal jumps. If you always land a little short vertically whatever the shape, it's a habit: chasing it by shrinking the height would only make the vertical aim twitchier.`;
  };
  // the area a fixed shape r would start from: the largest that fits
  const startFixed = (r) => {
    const su = (state.status && state.status.env && state.status.env.setup) || {};
    if (!(r > 0) || !su.tabletW) return null;
    const w = Math.min(su.tabletW, su.tabletH / r);
    return { w, h: w * r };
  };

  function diagStart(c) {
    const env = (state.status && state.status.env) || {};
    const su = env.setup || {};
    const screen = su.display && su.display.w ? su.display.h / su.display.w : 9 / 16;
    if (state.diagShape == null) state.diagShape = "screen";
    if (state.diagFrom == null) state.diagFrom = "full";
    if (state.diagRatio == null) {
      // the shape of the area of now, unless it is that of the screen: then 4:3
      const r = su.area && su.area.w ? su.area.h / su.area.w : 0;
      state.diagRatio = nfTrim(r > 0 && Math.abs(r - screen) > 0.01 ? r : 0.75, 3);
    }
    const full = state.diagShape === "screen" ? c.startLock : state.diagShape === "free" ? c.startFree : startFixed(ratioVal(state.diagRatio));
    const curA = su.area && su.area.w ? su.area : null;
    if (state.diagStartW == null) state.diagStartW = curA ? nfTrim(curA.w, 1) : "";
    const otdNote = c.liveOk
      ? `<div class="note-bar ok">${ICON.good}<span><b>OpenTabletDriver answers:</b> I can set the area between maps.</span></div>`
      : `<div class="note-bar">${ICON.warn}<span><b>OpenTabletDriver doesn't answer.</b> The diagnosis sets the area between maps: OpenTabletDriver 0.6 must be running.</span></div>`;
    const tab = env.setup && env.setup.tabletName;
    const conn = env.connected;
    const tabNote = tab ? `<p class="small" style="margin-top:10px">Tablet: <b>${esc(tab)}</b>${conn ? (conn.includes(tab) ? " · connected" : ` · <span style="color:var(--bad)">doesn't look connected${conn.length ? ` (connected: ${esc(conn.join(", "))})` : ""}: pick the right one in Settings</span>`) : ""}</p>` : "";
    const fromWords = {
      full: `It starts from ${full && full.w ? rectTxt(full) : "the whole tablet"}${(state.status.settings || {}).diagMaxW > 0 ? ", the largest of the widths allowed" : ", the whole tablet"}. It's the fastest way to learn the scale of your hand: from the first map I see how short you land. At first you hit almost nothing, and that's normal.`,
      current: `It starts from the area you have now in OpenTabletDriver${curA ? ` (${nfTrim(curA.w, 1)} mm wide, ${posTxt(curA)})` : ""}, with the shape chosen below. Good if you're already close, or to fine-tune it. It takes a few more maps to learn the scale of your hand, because it sees smaller differences.`,
      custom: "It starts from an area as wide as you type, centred where your area is now, with the shape chosen below.",
    }[state.diagFrom];
    return `<div class="panel diag-start" style="margin-top:24px">
        <div class="diag-steps">
          <div><span class="n">1</span><b>Press Start and play a map</b><p>Your own maps, as always. If you fail, press F2 on the game-over screen to save the replay (or use NoFail); in multiplayer press F2 on the results screen, because there osu! doesn't save it by itself. Nothing changes during the map.</p></div>
          <div><span class="n">2</span><b>At the end of the map I measure the replay</b><p>As soon as osu! saves it: whether you land long or short on jumps, how precise you are with that width, where the clicks land, whether the jumps come out turned. I work out the width with the fewest expected aim misses and the place where your aim comes out straightest.</p></div>
          <div><span class="n">3</span><b>You play the next map with the new area</b><p>I put it in OpenTabletDriver and save it. Map after map the changes shrink to fractions of a millimetre.</p></div>
        </div>
        ${otdNote}
        ${tabNote}
        <div class="row" style="margin-top:16px;align-items:flex-end;flex-wrap:wrap;gap:18px">
          <div class="field"><label>Start from</label>${fromSeg(state.diagFrom, "data-from")}</div>
          ${state.diagFrom === "custom" ? `<div class="field"><label for="adStartW">Width (mm)</label><input class="input" id="adStartW" value="${esc(state.diagStartW)}" inputmode="decimal" style="width:90px;text-align:center"></div>` : ""}
        </div>
        <p class="xs muted" style="margin-top:8px">${esc(fromWords)}</p>
        <div class="row" style="margin-top:14px;align-items:flex-end;flex-wrap:wrap;gap:18px">
          <div class="field"><label>Area shape</label>${shapeSeg(state.diagShape, "data-shape")}</div>
          ${state.diagShape === "fixed" ? `<div class="field"><label for="adRatio">Height / width</label><input class="input" id="adRatio" value="${esc(state.diagRatio)}" inputmode="decimal" style="width:84px;text-align:center"></div>` : ""}
        </div>
        <p class="xs muted" style="margin-top:8px">${esc(shapeWords(state.diagShape, ratioVal(state.diagRatio), full && full.w ? full.h / full.w : 0, screen))} You can also change it later, during the diagnosis, without losing what it has measured.</p>
        <div class="row" style="margin-top:14px;align-items:flex-end;flex-wrap:wrap;gap:18px">${rangeField("adRange")}</div>
        <p class="xs muted" style="margin-top:8px">${esc(rangeWords())}</p>
        <div class="row" style="margin-top:18px"><button class="btn primary big" id="adStart" ${c.liveOk ? "" : "disabled"}>Start the diagnosis</button></div>
      </div>`;
  }

  function diagLive(c) {
    const a = c.adapt;
    const f = a.fit || {};
    const t = f.target || {};
    const locked = a.locked;
    const maps = a.maps || [];
    let head;
    if (!a.active) {
      head = `<div class="live-head"><span class="live-dot"></span><div><b>Paused</b><div class="muted small">${a.pauseNote ? esc(a.pauseNote) : "The area found stays in OpenTabletDriver. Press Resume whenever you like: it carries on from where it got to."}</div></div></div>`;
    } else {
      const lastPlayed = (a.maps || []).filter((m) => m.windows > 0).slice(-1)[0];
      const fw = (a.fit || {}).freeW;
      const blockTxt = !lastPlayed || !lastPlayed.blocked || !(lastPlayed.next && a.cur && Math.abs(lastPlayed.next.w - a.cur.w) < 0.05) ? ""
        : lastPlayed.blocked === "reach" ? "You play this one with the best area found: I don't try it bigger, because with your fastest maps you wouldn't reach the circles in time. The next one tries a little smaller. "
        : `You play this one with the best area found: I don't try it ${lastPlayed.blocked === "min" ? "smaller" : "bigger"}, because ${nfTrim(lastPlayed.limit, 1)} mm is the ${lastPlayed.blocked === "min" ? "minimum" : "maximum"} width you set${fw ? ` (without that limit the best would be about ${nfTrim(fw, 1)} mm)` : ""}. The next one tries a little ${lastPlayed.blocked === "min" ? "bigger" : "smaller"} again, by a different amount: that's why this width comes back every other map. `;
      const curIsNext = lastPlayed && lastPlayed.next && a.cur && Math.abs(lastPlayed.next.w - a.cur.w) < 0.05 && Math.abs(lastPlayed.next.x - a.cur.x) < 0.05;
      const pt = curIsNext ? userVec(lastPlayed.ptx || 0, lastPlayed.pty || 0, a.cur && a.cur.rot) : [0, 0];
      const placeTxt = Math.abs(pt[0]) >= 0.05 ? `I also move it ${nfTrim(Math.abs(pt[0]), 1)} mm ${pt[0] > 0 ? "right" : "left"} of the best place: every two maps I try a different side (right, left, below, above), to see where you're steadiest. `
        : Math.abs(pt[1]) >= 0.05 ? `I also move it ${nfTrim(Math.abs(pt[1]), 1)} mm ${pt[1] > 0 ? "lower" : "higher"} than the best place: every two maps I try a different side (right, left, below, above), to see where you're steadiest. ` : "";
      const tryTxt0 = a.try && Math.abs(a.try) >= 0.003 ? `You play this one ${nf(Math.abs(Math.exp(a.try) - 1) * 100, 1)}% ${a.try > 0 ? "bigger" : "smaller"} than the best area found: every map tries a slightly different width, and all of them help to find the right one. ` : blockTxt;
      const tryTxt = tryTxt0 + placeTxt;
      head = `<div class="live-head"><span class="live-dot on"></span><div><b>${a.nMaps ? "Play the next map" : "Play a map"}</b><div class="muted small">${tryTxt}The whole map with this area${a.nMaps ? "" : " (if you fail, press F2 on the game-over screen: it saves the replay of the part played)"}. At the end of the map, when osu! saves the replay, I work out the area for the next one and put it in OpenTabletDriver.${a.coach && a.coach.active ? (a.coach.isSet || a.coach.confirmed ? " The filter for this map is set." : " <b>Set the filter first</b>: the values are in The filter, below.") : ""}</div></div></div>`;
    }
    const lastM = maps.length ? maps[maps.length - 1] : null;
    let lastLine = `<span class="muted small">none yet: play a map (if you fail or you're in multiplayer, F2 at the end of the map)</span>`;
    if (lastM && !lastM.windows) {
      lastLine = `<span>${esc(lastM.title)}</span><span class="muted small">not used: ${esc(lastM.note || "too few measurable notes")}</span>`;
    } else if (lastM) {
      const after = lastM.held ? "no aim misses: area left as it was" : lastM.next && lastM.next.w ? changeWords(lastM.area, lastM.next, locked) : "";
      lastLine = `<span>${esc(mapWords(lastM))}</span><span class="muted small">${esc(lastM.title)}${lastM.failed ? " · failed, used up to where you got" : ""} · ${lastM.clicks || 0} clicks with ${esc(rectTxt(lastM.area))}${after ? ` · ${esc(after)}` : ""}</span>`;
    }
    const sd = Math.max(f.w ? f.w.sd : 0, locked ? 0 : (f.h ? f.h.sd : 0));
    const found = f.n >= 3 && t.w
      ? `<b class="readout">${rectTxt(t)}</b><span class="muted small">${posTxt(t)} · ${f.sure ? "reliable" : `still ±${mmTxt(Math.max(sd, 0.05))} mm`}${f.freeW ? ` · it's at the width limit you set: without it the best would be about ${nfTrim(f.freeW, 1)} mm (${nf(f.freeMiss, 1)} expected aim misses per 1000 notes instead of ${nf(f.missBest, 1)})` : ""}</span>`
      : `<span class="muted small">comes with the first map</span>`;
    const st = steadyInfo(a);
    const arc = arcInfo(a);
    const btns = `${a.active ? `<button class="btn small" id="adPause">Pause</button>` : `<button class="btn primary" id="adResume" ${c.liveOk ? "" : "disabled"}>Resume</button>`}
        <button class="btn small ghost" id="adRestore">Back to the previous area</button>`;
    const otdWin = a.otdWindow
      ? `<div class="otd-window"><div class="otd-window-text">${ICON.info}<span><b>The OpenTabletDriver window is open: its area numbers are old.</b> It keeps showing the area from when you opened it. Nothing to do: if you press Apply or Save there (for example to set the filter), I put the right area back by myself. Press the button only if you want to see the current numbers in it.</span></div><button class="btn" id="adOtdWin">Show the current area in OpenTabletDriver</button></div>`
      : "";
    const missNote = a.localModel ? " For this profile there aren't enough earlier replays to predict them from: I work them out from the diagnosis maps, and they get more precise map after map." : "";
    if (state.diagResetFrom == null) state.diagResetFrom = "full";
    if (state.diagResetW == null) state.diagResetW = a.cur && a.cur.w ? nfTrim(a.cur.w, 1) : "";
    const resetFroms = [["full", "Whole tablet"], ["found", "The area found"], ["current", "The previous area"], ["custom", "Pick the width"]];
    const shapeName = (sep) => ({ screen: "like the screen", fixed: `fixed${sep}${nfTrim(a.ratio, 3)}`, free: "free" })[a.shape] || "free";
    return `<div class="split" style="margin-top:24px">
      <div class="panel"><div class="chart" id="adTablet"></div>
        <div class="legend-row"><span class="swatch now"><i></i>In OpenTabletDriver now</span><span class="swatch next"><i></i>Area found</span><span class="swatch pf"><i></i>Before the diagnosis</span><span><i class="dot" style="--c:var(--text-3)"></i>centres used</span></div></div>
      <div class="panel live-now">
        ${otdWin}
        ${head}
        <div class="big-area readout">${rectTxt(a.cur)}<small>${posTxt(a.cur)}</small></div>
        ${perfBlock(a)}
        <div style="margin-top:12px">
          <div class="kv-line"><span>Last map</span>${lastLine}</div>
          <div class="kv-line"><span>Area found</span>${found}</div>
          ${f.missBest > 0 ? `<div class="kv-line"><span>Expected aim misses</span><b class="readout">${nf(f.missBest, 1)} per 1000 notes</b><span class="muted small">with the area found, over every kind of map you play${f.curW && t.w && Math.abs(f.curW - t.w) > 0.3 ? `; with the one of the last map (${nfTrim(f.curW, 1)} mm) ${nf(f.missCur, 1)}` : ""}${f.natural ? `; with ${nfTrim(f.natural, 1)} mm, where you'd land neither long nor short, ${nf(f.missNat, 1)}` : ""}. It's the width with the fewest aim misses: the chart further down shows them for every width.${missNote}</span></div>` : a.localModel || a.nMaps < 2 ? `<div class="kv-line"><span>Expected aim misses</span><span class="muted small">${a.nMaps < 2 ? "after a couple of maps" : "working them out from the diagnosis maps"}</span></div>` : ""}
          <div class="kv-line"><span>Rotation</span><b>${esc(arc.k)}</b><span class="muted small">${esc(arc.t)}</span></div>
          <div class="kv-line"><span>Steadiest position</span>${steadyLine(a)}</div>
          <div class="kv-line"><span>How much it changes</span><b>${esc(st.k)}</b><span class="muted small">${esc(st.t)}</span></div>
          <div class="kv-line"><span>Filter</span>${filterLine(a)}</div>
        </div>
        <div class="row" style="margin-top:14px;flex-wrap:wrap">${btns}</div>
        ${!a.active && !c.liveOk ? `<p class="xs muted" style="margin-top:8px">To resume, OpenTabletDriver 0.6 must be running.</p>` : ""}
        <div class="row" style="margin-top:16px;align-items:flex-end;gap:10px;flex-wrap:wrap">${rangeField("adRangeLive")}</div>
        <p class="xs muted" style="margin-top:4px">${esc(rangeWords())}${(state.status.settings || {}).diagMinW > 0 ? " If you change the limits, the area found is worked out again and I set it straight away." : ""}</p>
        <div class="row" style="margin-top:14px;align-items:center;gap:10px;flex-wrap:wrap"><span class="small">Position</span><div class="seg" role="group" aria-label="Area position"><button data-pos="pos-follow" class="${a.posFixed ? "" : "on"}" aria-pressed="${!a.posFixed}">Follows the hand</button><button data-pos="pos-here" class="${a.posFixed ? "on" : ""}" aria-pressed="${!!a.posFixed}">Hold here</button></div>${a.base && a.base.w && a.base.w < 0.9 * (a.rangeHi || 999) ? `<button class="btn small ghost" data-pos="pos-before">Hold where it was</button>` : ""}</div>
        <p class="xs muted" style="margin-top:4px">${a.posFixed ? `Held at ${esc(posTxt({ x: a.posX, y: a.posY }))}: only the size changes, around the spot where you rest your wrist.` : "I move it when moving it really changes where your clicks land, towards where you're steadiest, or sideways towards where your aim comes out straight (see Rotation). If your clicks stay off the same way wherever the area is (a habit: your wrist follows it), I don't chase it: it moves at most 1.5 mm per map and where you're steadiest is what counts. Where you always miss in the same spot (for example bottom right) I don't move or widen it to chase those misses. If it forces you to change where you rest your wrist, press Hold here: from then on only the size changes, and I stop trying other positions."}</p>
        <div class="row" style="margin-top:14px;align-items:center;gap:10px;flex-wrap:wrap"><span class="small">Shape</span>${shapeSeg(a.shape, "data-reshape")}${a.shape === "fixed" ? `<span class="small">height/width</span><input class="input" id="adRatioLive" value="${esc(nfTrim(a.ratio, 3))}" inputmode="decimal" aria-label="Height divided by width" style="width:72px;text-align:center"><button class="btn small ghost" id="adRatioSet">Use</button>` : ""}</div>
        <p class="xs muted" style="margin-top:4px">${esc(shapeWords(a.shape, a.ratio, a.shapeFrom, a.screenRatio))} Changing it deletes nothing: the area found is worked out again with the new rule and I set it straight away.</p>
        <div class="row" style="margin-top:12px;align-items:center;gap:10px"><span class="small">Keep the area if no aim miss</span><div class="seg" role="group" aria-label="Keep the area if no aim miss"><button data-hold="1" class="${a.holdClean ? "on" : ""}" aria-pressed="${!!a.holdClean}">On</button><button data-hold="0" class="${a.holdClean ? "" : "on"}" aria-pressed="${!a.holdClean}">Off</button></div></div>
        <p class="xs muted" style="margin-top:4px">${a.holdClean ? "If in a map you don't miss a single circle by clicking outside it, you play the next one with the same area. Misses from notelock or notes not pressed don't count. What I measured in that map I keep anyway." : "At the end of a map the area always changes, even if you didn't miss: clean maps also say which width you're most precise with. With On, after a map without aim misses I keep the same area."}</p>
        <div class="row" style="margin-top:12px;align-items:center;gap:10px;flex-wrap:wrap"><span class="small">Size</span><div class="seg" role="group" aria-label="Area size"><button data-size="size-free" class="${a.sizeFixed ? "" : "on"}" aria-pressed="${!a.sizeFixed}">Adapts</button><button data-size="size-fixed" class="${a.sizeFixed ? "on" : ""}" aria-pressed="${!!a.sizeFixed}">Fixed: only move it</button></div></div>
        <p class="xs muted" style="margin-top:4px">${a.sizeFixed ? `The size stays ${esc(rectTxt({ w: a.sizeW, h: a.sizeH }))} on every map: the analysis only decides where to put it (where your clicks land and where the aim comes out straight).` : `With every replay the area changes: the best found so far, a little bigger or smaller in turn and by varying amounts, so that the widths in between are played too (up to ${nf((a.tryMax || 0.08) * 100, 0)}%, less as the diagnosis gets sure). Every width tried says how precise and steady you are with it, and all of them count for the next area. Fixed: keeps the current size and only moves it.`}</p>
        <p class="xs muted" style="margin-top:12px">${a.nMaps === 1 ? "One map measured" : `${a.nMaps} maps measured`} (${a.n} stretches). Shape ${shapeName(" ")}. Before the diagnosis you had ${rectTxt(a.base)}.</p>
        <details class="why" style="margin-top:12px"><summary>How to play</summary>${DIAG_TIPS}</details>
        <details class="why" style="margin-top:6px" ${state.diagResetOpen ? "open" : ""} id="adResetBox"><summary>Start over</summary>
          <p class="small" style="margin-top:8px">Deletes the data collected and starts again, with the current shape (${shapeName(", ")}). The last area found stays on the Area page until you find another.</p>
          <div class="row" style="margin-top:10px;align-items:flex-end;flex-wrap:wrap;gap:14px">
            <div class="field"><label>Start again from</label>${fromSeg(state.diagResetFrom, "data-rfrom", resetFroms)}</div>
            ${state.diagResetFrom === "custom" ? `<div class="field"><label for="adResetW">Width (mm)</label><input class="input" id="adResetW" value="${esc(state.diagResetW)}" inputmode="decimal" style="width:90px;text-align:center"></div>` : ""}
            <button class="btn small ghost" id="adReset">Start over</button>
          </div>
        </details>
      </div></div>`;
  }

  function diagTrailChart(el, a, dim) {
    const tr = (a.fit && a.fit.trail) || [];
    if (tr.length < 2) return false;
    const xs = tr.map((_, i) => i + 1);
    // the area found now, against the areas used stretch by stretch
    const now = a.fit.target && a.fit.target[dim];
    const used = tr.map((p) => p.used[dim]), tgt = tr.map(() => now);
    const step = Math.max(1, [1, 2, 5, 10, 20, 50, 100].find((k) => xs.length / k <= 12) || 100);
    const xTicks = xs.filter((x) => x === 1 || x % step === 0);
    Charts.draw(el, Charts.line, {
      x: xs, xTicks, height: 220, ml: 50,
      series: [
        { name: "area used", values: used, color: "var(--now)", width: 1.6 },
        { name: "area found now", values: tgt, color: "var(--next)", width: 2, dash: true },
      ],
      xLabel: "stretches measured", xFmt: (v) => nf(v, 0), yFmt: (v) => nf(v, 0) + " mm", tipFmt: (v) => nf(v, 2) + " mm",
      tipX: (x) => `Stretch ${x}`,
      aria: dim === "w" ? "Area width stretch by stretch" : "Area height stretch by stretch",
    });
    return true;
  }

  // how steady the hand was with the widths measured, against what the
  // replays say
  function precWords(p) {
    if (!p || p.n < 4) return "";
    const g = p.gamma || 0, se = p.se || 1;
    const where = `measured on ${p.n} stretches between ${nfTrim(p.lo, 0)} and ${nfTrim(p.hi, 0)} mm`;
    if (g < -0.3 && g + se < 0.6) return `With smaller areas you're less precise than your replays say, with bigger ones more precise (${where}): I take that into account, and it pushes towards a bigger area.`;
    if (g > 0.3 && g - se > -0.6) return `With bigger areas you're less precise than your replays say, with smaller ones more precise (${where}): I take that into account, and it pushes towards a smaller area.`;
    return `Your precision with the different widths is as your replays predict (${where}).`;
  }

  function diagMissSection(a) {
    const f = a.fit || {};
    if (!(f.missCurve && f.missCurve.length > 2)) return "";
    return `<section class="section">${sectionHead("Expected aim misses by width", "For every width: how many circles you'd miss by clicking outside them per 1000 notes, over every kind of map you play. A small area needs a steadier hand (small circles, high CS, count), a big one needs faster movements (big, fast jumps count; beyond the speed you've ever reached I don't suggest it). It takes into account how long or short you land with each width (only the part that really changes with the area: a habit of landing short that no area corrects isn't chased) and how precise you are with the widths tried during the diagnosis.")}
      <div class="panel"><div class="chart" id="adMissChart"></div>
      <div class="legend-row"><span><i style="--c:var(--next)"></i>expected aim misses</span><span><i style="--c:var(--good)"></i>area found</span><span><i style="--c:var(--now)"></i>current area</span>${f.prec && f.prec.n >= 4 ? `<span><i style="--c:var(--line)"></i>widths measured</span>` : ""}</div>
      ${f.prec && f.prec.n >= 4 ? `<p class="small" style="margin-top:10px">${esc(precWords(f.prec))}</p>` : ""}</div></section>`;
  }

  function diagMissChart(el, a) {
    const f = a.fit || {};
    const c = f.missCurve || [];
    if (c.length < 3) return false;
    const xs = c.map((p) => p.w), ys = c.map((p) => p.miss);
    const vmarks = [];
    if (f.target && f.target.w) vmarks.push({ x: f.target.w, color: "var(--good)", width: 2, label: `found ${nfTrim(f.target.w, 1)} mm` });
    if (f.curW && Math.abs(f.curW - (f.target && f.target.w)) > 0.3) vmarks.push({ x: f.curW, color: "var(--now)", dash: true, label: `now ${nfTrim(f.curW, 1)}`, row: 1 });
    if (f.natural && Math.abs(f.natural - (f.target && f.target.w)) > 3) vmarks.push({ x: f.natural, color: "var(--text-3)", dash: true, label: `neither long nor short ${nfTrim(f.natural, 1)}`, row: 2 });
    const shade = f.prec && f.prec.n >= 4 && f.prec.hi > f.prec.lo ? [{ x0: f.prec.lo, x1: f.prec.hi, label: "measured" }] : [];
    Charts.draw(el, Charts.line, {
      x: xs, height: 240, ml: 50, vmarks, shade,
      series: [{ name: "expected aim misses", values: ys, color: "var(--next)", width: 2.2 }],
      xLabel: "area width (mm)", xFmt: (v) => nf(v, 0), yFmt: (v) => nf(v, 1), tipFmt: (v) => nf(v, 1) + " per 1000 notes",
      tipX: (x) => `${nfTrim(x, 1)} mm`, yLabel: "misses per 1000 notes",
      aria: "Expected aim misses by area width",
    });
    return true;
  }

  const rotTxt = (d) => Math.abs(d) < 0.05 ? "0°" : `${nf(Math.abs(d), 1)}° ${d > 0 ? "cw" : "ccw"}`;
  function diagMaps(a) {
    const all = a.maps || [];
    if (!all.length) return "";
    const anyFilter = all.some((m) => m.filter);
    const fname = (m) => { const k = m.filter; if (!k) return "–"; if (k === "off") return "none"; if (k === "mixed") return "changed"; if (k === "on" && m.fparams) return `outer ${nfTrim(m.fparams.outer, 2)} · inner ${nfTrim(m.fparams.inner, 2)}`; return k === "on" ? "Radial Follow" : k; };
    const rows = all.slice().reverse().map((m) => {
      const area = m.area && m.area.w ? rectTxt(m.area) : "–";
      const cols = 10 + (anyFilter ? 1 : 0);
      if (!m.windows) return `<tr><td>${esc(m.title)}</td><td class="readout">${area}</td><td colspan="${cols - 2}" class="small muted">not used: ${esc(m.note || "too few measurable notes")}</td></tr>`;
      const off = sidePair(...userVec(m.dx || 0, m.dy || 0, m.area && m.area.rot)).join(", ") || "centred";
      const after = m.held ? `<span class="tag ok" title="No aim misses: you played the next map with the same area">kept</span>` : m.next && m.next.w ? esc(changeWords(m.area, m.next, a.locked).replace(/^area /, "")) : "–";
      const tried = m.try && Math.abs(m.try) >= 0.003 ? ` <span class="tag" title="You played the next map a little ${m.try > 0 ? "bigger" : "smaller"} than the best area found, to try another width">${m.try > 0 ? "+" : "−"}${nf(Math.abs(Math.exp(m.try) - 1) * 100, 0)}%</span>` : "";
      const miss = `${m.aimMiss || 0}${m.exp > 0 ? ` <span class="muted xs">/ ${nf(m.exp, 1)}</span>` : ""}`;
      return `<tr><td>${esc(m.title)}${m.failed ? ` <span class="tag" title="Failed: replay saved with F2 on the game-over screen, used up to where you got">failed</span>` : ""}</td><td class="readout">${area}</td>${anyFilter ? `<td class="small">${esc(fname(m))}</td>` : ""}<td class="num">${m.clicks || 0}</td><td class="num">${miss}</td><td>${esc(gainTxt2(m.g || 0))}</td><td class="small">${esc(off)}</td><td class="num">${esc(rotTxt(m.rot || 0))}</td><td class="num">${m.line > 0 ? `${nf(m.line * 100, 1)}%` : "–"}</td><td class="num">${m.score > 0 ? `${nf(m.score, 0)}%` : "–"}</td><td class="small">${after}${tried}</td></tr>`;
    }).join("");
    return `<section class="section">${sectionHead("The maps played", "Every map with its area, from start to finish, measured on the replay osu! saves. Aim misses: circles missed by clicking outside them (notelock and notes not pressed don't count), and how many you'd usually have missed on those notes. Jumps: how long or short you land. Clicks: where they land against the centre of the circles. Rotation: how much the jumps come out turned. Curves: how far the line between one circle and the next strays from a straight one. Perfection: of the area found after that map. Next: how the area changed for the following map.")}
      <div class="table-scroll"><table class="data"><thead><tr><th>Map</th><th>Area</th>${anyFilter ? "<th>Filter</th>" : ""}<th class="num">Clicks</th><th class="num">Aim misses</th><th>Jumps</th><th>Clicks</th><th class="num">Rotation</th><th class="num">Curves</th><th class="num">Perfection</th><th>Next</th></tr></thead><tbody>${rows}</tbody></table></div></section>`;
  }

  // how close to a perfect area, map after map
  function diagPerfChart(el, a) {
    const ms = (a.maps || []).filter((m) => m.windows > 0 && m.score > 0);
    if (ms.length < 2) return false;
    const xs = ms.map((_, i) => i + 1);
    Charts.draw(el, Charts.line, {
      x: xs, height: 220, ml: 44, yMin: 0, yMax: 100,
      series: [
        { name: "perfection", values: ms.map((m) => m.score), color: "var(--next)", width: 2.2, dots: true },
        { name: "certainty", values: ms.map((m) => m.sure || 0), color: "var(--now)", width: 1.4, dash: true },
        { name: "clean aim", values: ms.map((m) => m.clean || 0), color: "var(--good)", width: 1.4, dash: true },
        ...(ms.some((m) => m.fpct != null) ? [{ name: "filter perfection", values: ms.map((m) => m.fpct != null ? m.fpct : null), color: "var(--warn)", width: 2, dots: true }] : []),
      ],
      xLabel: "maps", xFmt: (v) => nf(v, 0), yFmt: (v) => nf(v, 0) + "%", tipFmt: (v) => nf(v, 1) + "%",
      tipX: (x) => `${esc(ms[x - 1].title)}`, aria: "Area perfection map after map",
    });
    return true;
  }

  function pageTest(view) {
    const c = (state.status && state.status.calib) || {};
    const r = state.report;
    const ar = r && r.areas;
    const a = c.mode === "adapt" ? c.adapt : null;
    view.innerHTML = `<div class="page">
      <h1 class="h-page">Diagnosis</h1>
      <p class="lede">You play your own maps, each with a single area from start to finish. At the end of the map I measure from the replay whether you land long or short on jumps, how precise you are, where the clicks land and whether your aim comes out turned, and I put the area for the next map in OpenTabletDriver. Nothing changes during the map. It goes on as long as you like.</p>
      ${a ? diagLive(c) : diagStart(c)}
      ${a ? diagFilter(a) : ""}
      ${a ? diagMissSection(a) : ""}
      ${a ? `<section class="section">${sectionHead("How close you are to the perfect area", "Perfection: how sure the diagnosis is of the area found (certainty) times how clean the aim is with it (few aim misses and straight lines between circles). It grows with the maps: every width tried narrows the doubts, and your hand gets used to it. 100% can't be reached: it would mean never missing a circle. With the guided filter there is also the filter perfection.")}
        <div class="panel"><div class="chart" id="adPerfChart"></div>
        <div class="legend-row"><span><i style="--c:var(--next)"></i>perfection</span><span><i style="--c:var(--now)"></i>certainty</span><span><i style="--c:var(--good)"></i>clean aim</span>${(a.maps || []).some((m) => m.fpct != null) ? `<span><i style="--c:var(--warn)"></i>filter perfection</span>` : ""}</div></div></section>` : ""}
      ${a ? `<section class="section">${sectionHead("How the area adapted", "Each map is measured in stretches of 20–45 seconds: for each stretch the area you played with and, dashed, the area found now. At first it changes a lot, then it settles.")}
        <div class="panel"><h3>Width</h3><div class="chart" id="adChartW"></div>${a.locked ? "" : `<h3 style="margin-top:14px">Height</h3><div class="chart" id="adChartH"></div>`}
        <div class="legend-row"><span><i style="--c:var(--now)"></i>area you had</span><span><i style="--c:var(--next)"></i>area found now</span></div></div></section>` : ""}
      ${a ? diagMaps(a) : ""}
    </div>`;
    const on = (id, fn) => { const b = $("#" + id); if (b) b.addEventListener("click", fn); };
    const num = (el) => ratioVal(el && el.value);
    $$("[data-shape]", view).forEach((b) => b.addEventListener("click", () => { state.diagShape = b.dataset.shape; render(); }));
    $$("[data-from]", view).forEach((b) => b.addEventListener("click", () => { state.diagFrom = b.dataset.from; render(); }));
    $$("[data-rfrom]", view).forEach((b) => b.addEventListener("click", () => { state.diagResetFrom = b.dataset.rfrom; state.diagResetOpen = true; render(); }));
    { const el = $("#adRatio"); if (el) el.addEventListener("change", () => { state.diagRatio = el.value.trim(); render(); }); }
    { const el = $("#adStartW"); if (el) el.addEventListener("change", () => { state.diagStartW = el.value.trim(); }); }
    { const el = $("#adResetW"); if (el) el.addEventListener("change", () => { state.diagResetW = el.value.trim(); }); }
    { const box = $("#adResetBox"); if (box) box.addEventListener("toggle", () => { state.diagResetOpen = box.open; }); }
    for (const id of ["adRange", "adRangeLive"]) { const el = $("#" + id); if (el) el.addEventListener("change", () => saveRange(el)); }
    $$("[data-reshape]", view).forEach((b) => b.addEventListener("click", () => {
      const k = b.dataset.reshape;
      if (a && a.shape === k) return;
      const msg = { screen: "Shape like the screen", fixed: "Fixed shape: the one the area has now", free: "Free shape, starting from the current one" }[k];
      calibDo("shape-" + k, msg);
    }));
    on("adRatioSet", () => {
      const v = num($("#adRatioLive"));
      if (!(v >= 0.35 && v <= 1)) { toast("Type a number between 0.35 and 1 (height divided by width)"); return; }
      calibDo("shape-fixed:" + v, `Fixed shape: height/width ${nfTrim(v, 3)}`);
    });
    $$("[data-size]", view).forEach((b) => b.addEventListener("click", () => {
      if (!a || (b.dataset.size === "size-fixed") === !!a.sizeFixed) return;
      calibDo(b.dataset.size, b.dataset.size === "size-fixed" ? "Size fixed: the area only moves" : "The size adapts again");
    }));
    $$("[data-hold]", view).forEach((b) => b.addEventListener("click", async () => {
      try { await api("settings", { diagHold: b.dataset.hold === "1" }); await refreshStatus(); render(); } catch (e) { toast(e.message); }
    }));
    const startFrom = async (shape, ratio, from, wEl, wState, msg) => {
      let width = 0;
      if (from === "custom") {
        width = num(wEl) || ratioVal(wState);
        if (!(width > 0)) { toast("Type the width to start from, in mm"); return false; }
      }
      await api("calib", { action: "start", shape, ratio, start: from, width });
      toast(msg);
      await refreshStatus();
      render();
      return true;
    };
    on("adStart", async () => {
      try {
        const ri = $("#adRatio");
        if (ri) state.diagRatio = ri.value.trim();
        const r = ratioVal(state.diagRatio);
        if (state.diagShape === "fixed" && !(r >= 0.35 && r <= 1)) { toast("Type a number between 0.35 and 1 (height divided by width)"); return; }
        const wEl = $("#adStartW");
        if (wEl) state.diagStartW = wEl.value.trim();
        await startFrom(state.diagShape, state.diagShape === "fixed" ? r : 0, state.diagFrom, wEl, state.diagStartW, "Diagnosis running: play a map (if you fail, F2 on the game-over screen)");
      } catch (e) { toast(e.message); }
    });
    on("adPause", () => calibDo("pause", "Diagnosis paused: the area found stays in OpenTabletDriver"));
    on("adOtdWin", async (ev) => {
      ev.currentTarget.disabled = true;
      try { await api("otd-window", {}); toast("OpenTabletDriver window reopened: it now shows the current area. You don't need to do it after every map."); } catch (e) { toast(e.message); }
      setTimeout(async () => { await refreshStatus(); render(); }, 1500);
    });
    on("adResume", () => calibDo("resume", "Diagnosis resumed"));
    $$("[data-pos]", view).forEach((b) => b.addEventListener("click", () => {
      const msg = { "pos-follow": "The area position follows the hand", "pos-here": "Position held: only the size changes", "pos-before": "Position held where it was before the diagnosis" }[b.dataset.pos];
      calibDo(b.dataset.pos, msg);
    }));
    $$("[data-fcstart]", view).forEach((b) => b.addEventListener("click", () => {
      $$("[data-fcstart]", view).forEach((x) => { x.disabled = true; });
      calibDo(b.dataset.fcstart, "Guided filter: put the values for the next map in OpenTabletDriver");
    }));
    on("fcStop", () => calibDo("fc-stop", "Guided filter stopped: the filter in OpenTabletDriver stays as you set it"));
    on("fcConfirm", () => calibDo("fc-confirm", "OK: the next map counts with these values"));
    $$("[data-fc]", view).forEach((b) => b.addEventListener("click", () => calibDo(b.dataset.fc, {
      "fc-hold": "Keeping the best filter: no tries",
      "fc-tries": "Every map tries a slightly different filter",
      "fc-lowlag": "Lowest latency: the values for the next map have changed",
      "fc-normal": "More smoothing: the values for the next map have changed",
    }[b.dataset.fc])));
    on("adRestore", () => {
      if (!a || !confirm(`Put back in OpenTabletDriver the area you had before the diagnosis (${rectTxt(a.base)}) and pause? What the diagnosis has learnt is kept.`)) return;
      calibDo("restore", "Previous area put back");
    });
    on("adReset", async () => {
      if (!a) return;
      const from = state.diagResetFrom || "full";
      const wEl = $("#adResetW");
      if (wEl) state.diagResetW = wEl.value.trim();
      const what = { full: "from the whole tablet", found: `from the area found (${rectTxt(a.cur)})`, current: `from the area before the diagnosis (${rectTxt(a.base)})`, custom: `from ${state.diagResetW} mm of width` }[from];
      if (!confirm(`Start the diagnosis over, ${what}, with the current shape? The data collected so far will be deleted.`)) return;
      try {
        state.diagResetOpen = false;
        await startFrom(a.shape, a.shape === "fixed" ? a.ratio : 0, from, wEl, state.diagResetW, "Diagnosis started over");
      } catch (e) { toast(e.message); }
    });
    if (a && $("#adTablet")) {
      const tw = (ar && ar.tabletW) || (state.status.env.setup || {}).tabletW, th = (ar && ar.tabletH) || (state.status.env.setup || {}).tabletH;
      const f = a.fit || {};
      const areas = [{ rect: a.base, kind: "past" }];
      if (f.n >= 3 && f.target && f.target.w && !sameRect(f.target, a.cur)) areas.push({ rect: f.target, kind: "ideal", label: "found" });
      areas.push({ rect: a.cur, kind: "now", label: f.n >= 3 && sameRect(f.target, a.cur) ? "now · found" : "now" });
      const trail = ((f.trail) || []).map((p) => [p.used.x, p.used.y]);
      Charts.draw($("#adTablet"), Charts.tablet, { tw, th, areas, trail, maxHeight: 420 });
    }
    if (a && $("#adMissChart")) diagMissChart($("#adMissChart"), a);
    if (a && $("#adPerfChart") && !diagPerfChart($("#adPerfChart"), a)) $("#adPerfChart").innerHTML = `<p class="small muted">The chart comes after a couple of maps.</p>`;
    if (a && $("#adChartW") && !diagTrailChart($("#adChartW"), a, "w")) $("#adChartW").innerHTML = `<p class="small muted">The chart comes after the first map.</p>`;
    if (a && $("#adChartH") && !diagTrailChart($("#adChartH"), a, "h")) $("#adChartH").innerHTML = "";
  }
  const sameRect = (x, y) => x && y && Math.abs(x.w - y.w) < 0.3 && Math.abs(x.h - y.h) < 0.3 && Math.hypot(x.x - y.x, x.y - y.y) < 0.5;

  /* ----- Settings ----- */
  function pageSettings(view) {
    const s = state.status;
    if (!s) { view.innerHTML = ""; return; }
    const env = s.env || {};
    const set = s.settings || {};
    const g = env.game || {};
    const prof = env.profile;
    const epochs = (s.epochs || []).slice();
    const cur = epochs.find((e) => !e.to);
    const toLocalInput = (iso) => { const t = new Date(iso); const z = new Date(t.getTime() - t.getTimezoneOffset() * 60000); return z.toISOString().slice(0, 16); };
    const tabletOpts = (state.tablets || []).map((t) => `<option value="${esc(t.name)}" ${set.tablet && set.tablet.name === t.name ? "selected" : ""}>${esc(t.name)} (${nfTrim(t.w, 1)} × ${nfTrim(t.h, 1)} mm)</option>`).join("");
    const srcName = (e) => ({ manuale: "added by hand", osu: "osu! resolution", filtro: "filter change", calibrazione: "diagnosis" })[e.source] || "OpenTabletDriver";
    view.innerHTML = `<div class="page">
      <h1 class="h-page">Settings</h1>
      <p class="lede">Almost everything is detected by itself. Change something only if a value below is wrong.</p>
      ${warnList(env.warnings)}
      <div class="grid-2 section">
        <div class="panel"><h3>osu!</h3><p class="sub">${env.osuFound ? "Folder found" : "Folder not found"}</p>
          <dl class="kv"><dt>Folder</dt><dd>${esc(env.osuDir || "–")}</dd><dt>Configuration</dt><dd>${esc(env.cfgPath || "–")}</dd><dt>Resolution</dt><dd>${env.setup ? `${nf(env.setup.gameW, 0)} × ${nf(env.setup.gameH, 0)}` : "–"} ${g.fullscreen ? "fullscreen" : "windowed"}</dd><dt>Screen</dt><dd>${nf(env.screenW, 0)} × ${nf(env.screenH, 0)}</dd><dt>Sensitivity</dt><dd>${g.mouseSpeed != null ? nf(g.mouseSpeed, 2) : "–"}</dd><dt>Maps indexed</dt><dd>${nf(env.mapsIndexed, 0)}</dd></dl>
          <div class="field" style="margin-top:14px"><label for="osuDir">osu! folder (empty = automatic)</label><div class="row"><input class="input" id="osuDir" value="${esc(set.osuDir || "")}" placeholder="${esc(env.osuDir || "C:\\Users\\…\\AppData\\Local\\osu!")}" style="flex:1"><button class="btn" data-save="osuDir">Save</button></div></div>
          <div class="row" style="margin-top:12px">
            <div class="field" style="flex:1"><label for="gameW">Game resolution (empty = automatic)</label><div class="row"><input class="input" id="gameW" inputmode="numeric" value="${set.gameW || ""}" placeholder="${env.setup?.gameW ? Math.round(env.setup.gameW) : ""}" style="width:100px"><span class="muted">×</span><input class="input" id="gameH" inputmode="numeric" value="${set.gameH || ""}" placeholder="${env.setup?.gameH ? Math.round(env.setup.gameH) : ""}" style="width:100px"><button class="btn" id="saveRes">Save</button></div></div>
          </div>
        </div>
        <div class="panel"><h3>OpenTabletDriver</h3><p class="sub">${env.otdFound ? "Settings read" : "Settings not found"}</p>
          <dl class="kv"><dt>File</dt><dd>${esc(env.otdPath || "–")}</dd><dt>Tablet</dt><dd>${esc(prof ? prof.tablet : "–")}</dd><dt>Active surface</dt><dd>${env.setup && env.setup.tabletW ? `${nfTrim(env.setup.tabletW, 1)} × ${nfTrim(env.setup.tabletH, 1)} mm` : "unknown"}</dd><dt>Area</dt><dd>${prof ? `${nfTrim(prof.area.w)} × ${nfTrim(prof.area.h)} mm, centre ${nfTrim(prof.area.x)}, ${nfTrim(prof.area.y)}, ${nfTrim(prof.area.rot)}°` : "–"}</dd><dt>Screen mapped</dt><dd>${prof ? `${nfTrim(prof.display.w)} × ${nfTrim(prof.display.h)} px, centre ${nfTrim(prof.display.x)}, ${nfTrim(prof.display.y)}` : "–"}</dd><dt>Mode</dt><dd>${prof ? (prof.absolute ? "absolute" : "relative") : "–"}</dd></dl>
          <div class="field" style="margin-top:14px"><label for="otdPath">settings.json file (empty = automatic)</label><div class="row"><input class="input" id="otdPath" value="${esc(set.otdPath || "")}" placeholder="${esc(env.otdPath || "%LOCALAPPDATA%\\OpenTabletDriver\\settings.json")}" style="flex:1"><button class="btn" data-save="otdPath">Save</button></div></div>
          ${(env.profiles || []).length > 1 ? `<div class="field" style="margin-top:12px"><label for="prefTab">Profile to use</label><select class="input" id="prefTab">${env.profiles.map((p) => `<option ${p.tablet === (set.preferredTablet || (prof && prof.tablet)) ? "selected" : ""}>${esc(p.tablet)}</option>`).join("")}</select></div>` : ""}
          <div class="field" style="margin-top:12px"><label for="tabSel">Tablet model (if the size is wrong)</label><select class="input" id="tabSel"><option value="">Automatic</option>${tabletOpts}</select></div>
        </div>
      </div>
      <section class="section" id="profiles">${sectionHead("Grip profiles", "Every replay belongs to a profile. Analysis, suggested area, filter and diagnosis only use the replays of the active profile. When you change grip, create a new profile: it starts from zero. Going back to the previous profile you find everything as it was.")}
        <div class="panel">
          <div class="table-scroll"><table class="data"><thead><tr><th>Name</th><th class="num">Replays</th><th>Created</th><th></th></tr></thead><tbody>${(s.profiles || []).map((p) => `<tr class="prof-row"><td><div class="row" style="gap:8px"><input class="input" data-pname="${p.id}" value="${esc(p.name)}" maxlength="40" aria-label="Profile name"><button class="btn small ghost" data-prename="${p.id}">Rename</button></div></td><td class="num">${nf(p.plays, 0)}</td><td class="muted">${dateFmt.format(d(p.created))}</td><td>${p.active ? '<span class="tag cur">active</span>' : `<button class="btn small" data-puse="${p.id}">Use this</button>`}</td></tr>`).join("")}</tbody></table></div>
          <div class="row" style="margin-top:14px;align-items:flex-end">
            <div class="field"><label for="newProfile">New profile</label><input class="input" id="newProfile" maxlength="40" placeholder="e.g. Palm grip" style="width:240px"></div>
            <button class="btn primary" id="addProfile">Create and use</button>
          </div>
          <p class="xs muted" style="margin-top:10px">The plays made from now on go to the active profile. If a play ended up in the wrong profile, change it from its details on the Plays page.</p>
        </div>
      </section>
      <section class="section">${sectionHead("Area history", "Plays are assigned to the area in use at that moment. If the change was detected late, move the date earlier: more plays go into the analysis.")}
        <div class="panel">
          ${cur ? `<div class="row" style="align-items:flex-end;gap:16px">
            <div class="field"><label for="curFrom">I've used the current area since</label><input class="input" type="datetime-local" id="curFrom" value="${toLocalInput(cur.from)}"></div>
            <button class="btn" id="saveFrom">Save the date</button>
            <label class="row small" style="gap:8px;margin-left:auto"><input type="checkbox" id="adapted" ${cur.adapted || cur.established ? "checked" : ""} ${cur.established ? "disabled" : ""}> I'm already used to this area</label>
          </div>` : `<p class="muted small">No area recorded.</p>`}
          <div class="table-scroll"><table class="data" style="margin-top:16px"><thead><tr><th>From</th><th>To</th><th>Area</th><th>Source</th><th></th></tr></thead><tbody>${epochs.slice().reverse().map((e) => `<tr><td>${dateTime.format(d(e.from))}</td><td>${e.to ? dateTime.format(d(e.to)) : '<span class="tag cur">in use</span>'}</td><td class="readout">${nfTrim(e.setup.area.w)} × ${nfTrim(e.setup.area.h)} mm · ${nfTrim(e.setup.area.x)}, ${nfTrim(e.setup.area.y)}</td><td class="muted">${srcName(e)}</td><td>${e.source === "manuale" ? `<button class="btn small ghost" data-rm="${e.id}">Delete</button>` : ""}</td></tr>`).join("")}</tbody></table></div>
        </div>
      </section>
      <div class="grid-2 section">
        <div class="panel"><h3>Data</h3><p class="sub">Analysed replays, history and settings are saved here.</p>
          <dl class="kv"><dt>Folder</dt><dd>${esc(s.dataDir)}</dd><dt>Plays</dt><dd>${nf(s.plays, 0)}</dd><dt>Replays used</dt><dd>${s.firstRun ? `saved since ${dateTime.format(d(s.firstRun))}, the first start` : "–"}</dd></dl>
          <div class="row" style="margin-top:14px"><button class="btn" id="openData">${ICON.folder}Open the folder</button><button class="btn ghost" id="reimport">Re-import all replays</button></div>
        </div>
        <div class="panel"><h3>Appearance</h3><p class="sub">Interface theme.</p>
          <div class="seg" role="group" aria-label="Theme">${[["", "System"], ["dark", "Dark"], ["light", "Light"]].map(([v, l]) => `<button data-theme-v="${v}" class="${(set.theme || "") === v ? "on" : ""}">${l}</button>`).join("")}</div>
          <p class="xs muted" style="margin-top:16px">Area Coach ${esc(s.version)}</p>
        </div>
      </div>
    </div>`;
    const save = async (obj, msg = "Settings saved") => {
      try { await api("settings", obj); toast(msg); setTimeout(refreshStatus, 600); } catch (e) { toast(e.message); }
    };
    $$("[data-save]", view).forEach((b) => b.addEventListener("click", () => {
      const k = b.dataset.save;
      let v = $("#" + k).value.trim();
      if (b.dataset.num) v = parseInt(v, 10) || 0;
      save({ [k]: v });
    }));
    $("#saveRes").addEventListener("click", () => save({ gameW: parseInt($("#gameW").value, 10) || 0, gameH: parseInt($("#gameH").value, 10) || 0 }));
    const pt = $("#prefTab");
    if (pt) pt.addEventListener("change", () => save({ preferredTablet: pt.value }));
    $("#tabSel").addEventListener("change", (e) => {
      const t = (state.tablets || []).find((x) => x.name === e.target.value);
      save({ tablet: t ? { name: t.name, w: t.w, h: t.h } : null });
    });
    const sf = $("#saveFrom");
    if (sf) sf.addEventListener("click", async () => {
      try { await api("epoch", { action: "start", id: cur.id, from: new Date($("#curFrom").value).toISOString() }); toast("Date saved: recalculating"); refreshStatus(); } catch (e) { toast(e.message); }
    });
    const ad = $("#adapted");
    if (ad && !ad.disabled) ad.addEventListener("change", async () => { await api("epoch", { action: "adapted", id: cur.id, adapted: ad.checked }); toast("Saved"); refreshStatus(); });
    $$("[data-rm]", view).forEach((b) => b.addEventListener("click", async () => { try { await api("epoch", { action: "remove", id: +b.dataset.rm }); refreshStatus(); } catch (e) { toast(e.message); } }));
    $$("[data-puse]", view).forEach((b) => b.addEventListener("click", () => switchProfile(+b.dataset.puse)));
    $$("[data-prename]", view).forEach((b) => b.addEventListener("click", async () => {
      const id = +b.dataset.prename;
      const name = $(`[data-pname="${id}"]`, view).value.trim();
      try { await api("profile", { action: "rename", id, name }); toast("Name saved"); await refreshStatus(); render(); } catch (e) { toast(e.message); }
    }));
    $("#addProfile").addEventListener("click", async () => {
      const name = $("#newProfile").value.trim();
      if (!name) { toast("Type a name for the profile"); $("#newProfile").focus(); return; }
      try {
        await api("profile", { action: "create", name });
        toast(`Profile "${name}" created and active: starting from zero`);
        state.version = -1;
        await refreshStatus();
        render();
      } catch (e) { toast(e.message); }
    });
    $("#openData").addEventListener("click", () => api("open", { what: "data" }));
    $("#reimport").addEventListener("click", async () => {
      if (!confirm("Analyse all the replays again from scratch? The area history is kept.")) return;
      await api("rescan", { full: true });
      toast("Re-import started");
    });
    $$("[data-theme-v]", view).forEach((b) => b.addEventListener("click", () => save({ theme: b.dataset.themeV }, "Theme updated")));
  }

  /* ---------------- boot ---------------- */
  async function onRoute() {
    document.querySelectorAll(".drawer, .drawer-bg").forEach((el) => el.remove());
    const route = currentRoute();
    if (route === "plays") await loadPlays(true).catch(() => {});
    if (route === "filter") {
      state.filter = await api("filter").catch(() => null);
      if (state.filter && !state.filter.result && !state.filter.running && !state.filter.error) state.filter = await api("filter", { action: "run" }).catch(() => state.filter);
    }
    if (route === "settings" && !state.tablets) state.tablets = await api("tablets").catch(() => []);
    render();
    $("#view").scrollTop = 0;
  }

  window.addEventListener("hashchange", onRoute);
  let statusTimer = null;
  function connect() {
    const es = new EventSource("/api/events?t=" + encodeURIComponent(token));
    es.addEventListener("status", () => refreshStatus());
    es.addEventListener("import", () => { clearTimeout(statusTimer); statusTimer = setTimeout(refreshStatus, 250); });
    es.addEventListener("dirty", () => setTimeout(refreshReport, 300));
    es.addEventListener("calib", async () => {
      const prev = state.status?.calib;
      await refreshStatus();
      const c = state.status?.calib;
      if (c && c.mode === "adapt") {
        if (prev && prev.adapt && prev.adapt.active && !c.adapt.active) toast(c.adapt.pauseNote ? `Diagnosis paused: ${c.adapt.pauseNote}` : "Diagnosis paused: the area found stays in OpenTabletDriver");
        const pm = (prev && prev.adapt && prev.adapt.maps) || [], nm = (c.adapt && c.adapt.maps) || [];
        if (prev && prev.adapt && c.adapt.active && nm.length && (nm.length > pm.length || nm[nm.length - 1].playId !== (pm[pm.length - 1] || {}).playId)) {
          const m = nm[nm.length - 1];
          toast(m.windows ? `${m.title}${m.failed ? " (failed)" : ""}: measured. You play the next one with ${areaTxt(c.adapt.cur)}` : `${m.title}: not used (${m.note || "too few notes"})`);
        }
      }
      if (state.route === "diagnosis" || state.route === "area") render();
    });
    es.addEventListener("filter", async () => {
      if (state.route !== "filter") return;
      state.filter = await api("filter").catch(() => state.filter);
      render();
    });
    es.addEventListener("report", async () => {
      await refreshReport();
      if (state.route === "plays") { await loadPlays(true); render(); }
      if (state.route === "diagnosis") render();
    });
    es.onerror = () => { es.close(); setTimeout(connect, 3000); };
  }
  (async function boot() {
    await refreshStatus();
    await refreshReport();
    await onRoute();
    connect();
    setInterval(refreshStatus, 15000);
  })();
})();
