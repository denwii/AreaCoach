"use strict";

const token = new URLSearchParams(location.search).get("t") || "";
let S = null;
let lastJSON = "";
let tab = "replay";
try { tab = localStorage.getItem("tab") || "replay"; } catch (e) { /* no storage */ }
let pending = false;

// ---------- server ----------
async function getState() {
  const r = await fetch("api/state?t=" + encodeURIComponent(token));
  if (!r.ok) throw new Error("the program doesn't answer");
  return r.json();
}
async function post(path, body) {
  const r = await fetch("api/" + path + "?t=" + encodeURIComponent(token), {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body || {}),
  });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || "something went wrong");
  return j;
}
async function act(path, body, okMsg) {
  if (pending) return;
  pending = true;
  render();
  try {
    const st = await post(path, body);
    setState(st, true);
    if (okMsg) toast(okMsg);
  } catch (e) {
    toast(e.message, true);
  } finally {
    pending = false;
    render();
  }
}

// ---------- helpers ----------
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const f = (v, d = 2) => (v == null || !isFinite(v) ? "–" : Number(v).toFixed(d));
const trim = (v, d = 3) => (v == null || !isFinite(v) ? "" : String(Number(Number(v).toFixed(d))));
const sgn = (v, d = 2) => (v > 0 ? "+" : v < 0 ? "−" : "±") + Math.abs(v).toFixed(d);
const pct = (v) => Math.round(Math.abs(v) * 100) + "%";
const ago = (t) => {
  const s = (Date.now() - new Date(t).getTime()) / 1000;
  if (s < 60) return "just now";
  if (s < 3600) return Math.round(s / 60) + " min ago";
  if (s < 86400) return Math.round(s / 3600) + " h ago";
  return new Date(t).toLocaleString();
};
const areaTxt = (r) => `${f(r.w)} × ${f(r.h, 3)} mm · X ${f(r.x)} · Y ${f(r.y)}${Math.abs(r.rot) > 0.01 ? ` · ${trim(r.rot, 2)}°` : ""}`;

let toastTimer = 0;
function toast(msg, err) {
  const el = document.getElementById("toast");
  el.textContent = msg;
  el.className = "toast" + (err ? " err" : "");
  el.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.hidden = true; }, err ? 6000 : 3000);
}

// ---------- figures ----------
function corners(r) {
  const t = (r.rot * Math.PI) / 180, c = Math.cos(t), s = Math.sin(t);
  return [[-1, -1], [1, -1], [1, 1], [-1, 1]].map(([a, b]) => {
    const x = (a * r.w) / 2, y = (b * r.h) / 2;
    return [r.x + c * x - s * y, r.y + s * x + c * y];
  });
}
function tabletFig(areas) {
  let tw = S.tabletW, th = S.tabletH;
  if (!(tw > 0 && th > 0)) {
    const pts = areas.flatMap((a) => corners(a.r));
    tw = Math.max(...pts.map((p) => p[0])) * 1.15;
    th = Math.max(...pts.map((p) => p[1])) * 1.15;
  }
  const pad = Math.max(tw, th) * 0.03;
  const poly = (r) => corners(r).map((p) => p.map((v) => v.toFixed(2)).join(",")).join(" ");
  let g = `<rect x="0" y="0" width="${tw}" height="${th}" rx="${pad}" fill="var(--surface2)" stroke="var(--border)" stroke-width="${tw / 300}"/>`;
  for (const a of areas) {
    const sw = tw / 260;
    if (a.kind === "now") g += `<polygon points="${poly(a.r)}" fill="none" stroke="var(--muted)" stroke-width="${sw}" stroke-dasharray="${sw * 4} ${sw * 3}"/>`;
    else g += `<polygon points="${poly(a.r)}" fill="var(--accent-soft)" stroke="var(--accent)" stroke-width="${sw * 1.4}"/>`;
    g += `<circle cx="${a.r.x}" cy="${a.r.y}" r="${tw / 160}" fill="${a.kind === "now" ? "var(--muted)" : "var(--accent)"}"/>`;
  }
  return `<svg class="fig" viewBox="${-pad} ${-pad} ${tw + 2 * pad} ${th + 2 * pad}" role="img" aria-label="Tablet with the areas">${g}</svg>`;
}
function scatterFig(rep) {
  const res = rep.result;
  let g = `<circle cx="0" cy="0" r="1" fill="var(--accent-soft)" stroke="var(--accent)" stroke-width="0.02"/>`;
  g += `<line x1="-2.2" y1="0" x2="2.2" y2="0" stroke="var(--border)" stroke-width="0.01"/><line x1="0" y1="-2.2" x2="0" y2="2.2" stroke="var(--border)" stroke-width="0.01"/>`;
  for (const d of rep.dots || []) g += `<circle cx="${d[0].toFixed(3)}" cy="${d[1].toFixed(3)}" r="0.028" fill="var(--dot)"/>`;
  const b = res.before, a = res.after;
  g += `<g stroke="var(--bad)" stroke-width="0.035"><line x1="${b.offX - 0.09}" y1="${b.offY}" x2="${b.offX + 0.09}" y2="${b.offY}"/><line x1="${b.offX}" y1="${b.offY - 0.09}" x2="${b.offX}" y2="${b.offY + 0.09}"/></g>`;
  g += `<circle cx="${a.offX}" cy="${a.offY}" r="0.06" fill="none" stroke="var(--good)" stroke-width="0.035"/>`;
  return `<svg class="fig round" viewBox="-2.2 -2.2 4.4 4.4" role="img" aria-label="Where you pressed around the circles">${g}</svg>`;
}

// ---------- advice words ----------
// the changes worth saying: bigger than 0.05 mm and than what the map can
// tell (its "±")
function adviceWords(u, s, se) {
  se = se || [0, 0, 0];
  const dW = s.w - u.w, dX = s.x - u.x, dY = s.y - u.y;
  const big = (d, e) => Math.abs(d) >= Math.max(0.05, e || 0);
  const parts = [];
  if (big(dW, se[0])) parts.push(`make it ${f(Math.abs(dW))} mm ${dW > 0 ? "wider" : "narrower"}`);
  const mv = [];
  if (big(dX, se[1])) mv.push(`${f(Math.abs(dX))} mm ${dX > 0 ? "right" : "left"}`);
  if (big(dY, se[2])) mv.push(`${f(Math.abs(dY))} mm ${dY > 0 ? "down" : "up"}`);
  if (mv.length) parts.push("move it " + mv.join(" and "));
  if (!parts.length && Math.abs(s.h - u.h) >= 0.05) parts.push(`set the height to ${f(s.h, 3)} mm to keep the shape`);
  if (!parts.length) return null;
  const t = parts.join(", then ");
  return t.charAt(0).toUpperCase() + t.slice(1) + ".";
}
function offWords(st) {
  const p = [];
  if (Math.abs(st.offX) >= 0.03) p.push(`${pct(st.offX)} ${st.offX > 0 ? "right" : "left"}`);
  if (Math.abs(st.offY) >= 0.03) p.push(`${pct(st.offY)} ${st.offY > 0 ? "low" : "high"}`);
  return p.length ? p.join(", ") : "centred";
}

// ---------- views ----------
function viewReplay() {
  const rep = S.report;
  const busy = S.busy || pending;
  const analyzeBtn = (primary) => `<button class="btn${primary ? " primary" : ""}" data-act="analyze" ${busy ? "disabled" : ""}>${busy ? "Looking…" : "Analyse my latest replay"}</button>`;
  const lz = S.lazerScan < 1 ? `<div class="note">Reading osu!lazer's files for the first time: ${Math.round(S.lazerScan * 100)}%</div>` : "";
  if (!rep) {
    return `<div class="card empty"><div class="big">Waiting for your next replay</div>
      <p>Play a map to the end in osu! or osu!lazer: as soon as the replay is saved, the advice for your area shows up here. Only that play counts.</p>
      ${analyzeBtn(true)}${lz}${S.replayMsg ? `<div class="note">${esc(S.replayMsg)}</div>` : ""}</div>${safeNote()}`;
  }
  let head = `<div class="maphead"><span class="title">${esc(rep.map || "Replay")}</span>
    ${rep.mods ? `<span class="tag">${esc(rep.mods)}</span>` : ""}${rep.lazer ? `<span class="tag">lazer</span>` : ""}
    <span class="muted">${ago(rep.playedAt)}${rep.accuracy ? ` · ${f(rep.accuracy * 100, 2)}%` : ""}</span></div>`;
  if (rep.error) {
    return `<div class="card">${head}<div class="advice">No advice from this replay</div><div class="sub">${esc(rep.error)}.</div>
      <div class="btns">${analyzeBtn(false)}</div></div>${safeNote()}`;
  }
  const res = rep.result, u = rep.used, s = res.area;
  const words = adviceWords(u, s, res.se);
  const tiny = !words && !sameArea(u, s);
  const row = (k, a, b, d) => `<tr><td>${k}</td><td class="num">${f(a, d)}</td><td class="num new">${f(b, d)}<span class="d">${Math.abs(b - a) >= 0.005 ? sgn(b - a, d) : ""}</span></td></tr>`;
  const applied = rep.appliedNow || (S.area && sameArea(S.area, s));
  const notes = [];
  if (rep.usedGuess) notes.push("This replay is from before Area Coach was open: I assumed you played it with the area in use now.");
  if (rep.widthKept) notes.push(`The notes of this map don't tell the width well (± ${f(res.se[0], 1)} mm): I only moved the area. A map that uses the whole screen tells it.`);
  if (rep.mixed) notes.push("The area changed during this play: each click is counted with the area it was played with.");
  if (res.atMax) notes.push("The width is at the top of your range (Settings): a wider area would fit better.");
  if (res.atMin) notes.push("The width is at the bottom of your range (Settings): a narrower area would fit better.");
  const M = S.memory;
  if (M && M.hasBand && (s.w < M.bandLo - 0.05 || s.w > M.bandHi + 0.05)) notes.push(`This width is outside the band the live calibration found for you (${trim(M.bandLo, 1)}–${trim(M.bandHi, 1)} mm): one map says this, many maps said that.`);
  if (rep.played < rep.heads) notes.push(`You stopped playing ${rep.heads - rep.played} notes before the end: those aren't counted.`);
  if (S.area && !sameArea(S.area, u) && !applied) notes.push(`The area in OpenTabletDriver changed after this play (now ${areaTxt(S.area)}).`);
  const se = res.se || [0, 0, 0];
  const missPct = (st) => (st.clicks ? ` <small>${f((100 * st.misses) / st.clicks, 1)}%</small>` : "");
  return `<div class="card">${head}
    <div class="advice${words ? "" : " ok"}">${words ? esc(words) : "Your area already fits this play: keep it."}</div>
    <div class="sub">${tiny ? "The differences below are smaller than what one map can tell: change them only if you like." : "The area with which the same hand movements put the most clicks inside the circles, and the closest to their centres."}</div>
    <table class="vals"><thead><tr><th></th><th>Played with</th><th>Suggested</th></tr></thead><tbody>
      ${row("Width", u.w, s.w, 2)}${row("Height", u.h, s.h, 3)}${row("X (centre)", u.x, s.x, 2)}${row("Y (centre)", u.y, s.y, 2)}
    </tbody></table>
    <div class="stats">
      <div class="stat"><div class="k">Clicks outside the circle</div><div class="v num">${res.before.misses} → ${res.after.misses}${missPct(res.after)}</div><div class="k">of ${res.before.clicks} clicks, same movements</div></div>
      <div class="stat"><div class="k">Where you press, from the centre</div><div class="v">${offWords(res.before)} → ${offWords(res.after)}</div><div class="k">in % of the circle radius</div></div>
      <div class="stat"><div class="k">How sure, from this map</div><div class="v num">± ${f(se[0], 1)} <small>mm width</small></div><div class="k">± ${f(Math.max(se[1], se[2]), 1)} mm position</div></div>
    </div>
    <div class="btns">
      ${applied ? `<span class="tag" style="padding:6px 12px">In OpenTabletDriver ✓</span>${S.undo ? `<button class="btn" data-act="undo">Go back to ${f(S.undo.w)} × ${f(S.undo.h, 3)}</button>` : ""}<button class="btn" data-act="reopen">Reopen the OpenTabletDriver window</button>`
        : `<button class="btn primary" data-act="apply" data-id="${rep.id}" ${pending ? "disabled" : ""}>Put this area in OpenTabletDriver</button>`}
      ${analyzeBtn(false)}
    </div>
    ${applied && S.note ? `<div class="note warn">${esc(S.note)}</div>` : ""}
    ${notes.map((n) => `<div class="note">${esc(n)}</div>`).join("")}${S.replayMsg ? `<div class="note warn">${esc(S.replayMsg)}</div>` : ""}
  </div>
  <div class="grid">
    <div class="card"><h2>On the tablet</h2>${tabletFig([{ r: u, kind: "now" }, { r: s, kind: "new" }])}
      <div class="legend"><span><span class="sw" style="border:1.5px dashed var(--muted)"></span>Played with</span><span><span class="sw" style="background:var(--accent-soft);border:1.5px solid var(--accent)"></span>Suggested</span></div></div>
    <div class="card"><h2>Where you pressed</h2>${scatterFig(rep)}
      <div class="legend"><span><span class="sw" style="background:var(--accent-soft);border:1.5px solid var(--accent);border-radius:50%"></span>The circle</span><span><span style="color:var(--bad);font-weight:700">+</span> Your average</span><span><span style="color:var(--good);font-weight:700">○</span> With the suggested area</span></div></div>
  </div>${safeNote()}`;
}
function safeNote() {
  return `<div class="note" style="margin-top:16px">This section only reads the replay file osu! saves after a map, and changes the area only when you press the button: the same as setting it by hand in OpenTabletDriver.</div>`;
}
function sameArea(a, b) {
  return Math.abs(a.w - b.w) < 0.006 && Math.abs(a.h - b.h) < 0.006 && Math.abs(a.x - b.x) < 0.006 && Math.abs(a.y - b.y) < 0.006;
}

function viewLive() {
  const L = S.live;
  const phase = {
    off: ["", "Off"],
    noosu: ["wait", "Open osu! (stable or lazer)"],
    menu: ["wait", "Waiting for a map to start"],
    nomap: ["wait", `I can't find this map in your songs: ${esc(L.map)}`],
    syncing: ["wait", `Lining up with ${esc(L.map)}… keep playing`],
    playing: ["on", `Following ${esc(L.map)}`],
  }[L.phase] || ["", esc(L.phase)];
  const ss = L.session || {};
  let detail = "";
  if (L.phase === "playing") {
    const mods = [];
    if (ss.rate && Math.abs(ss.rate - 1) > 0.001) mods.push(`${trim(ss.rate, 2)}× speed`);
    if (ss.hr) mods.push("HR");
    detail = `${ss.clicks} clicks${ss.clicks ? `, ${Math.round((100 * ss.hits) / ss.clicks)}% inside` : ""}${mods.length ? " · " + mods.join(" · ") : ""}`;
  }
  const steps = (L.steps || []).slice().reverse();
  const stepRows = steps.slice(0, 60).map((st) => {
    const a = st.from, b = st.to;
    const mv = [];
    if (Math.abs(b.w - a.w) >= 0.005) mv.push(`W ${sgn(b.w - a.w)}`);
    if (Math.abs(b.x - a.x) >= 0.005) mv.push(`X ${sgn(b.x - a.x)}`);
    if (Math.abs(b.y - a.y) >= 0.005) mv.push(`Y ${sgn(b.y - a.y)}`);
    return `<tr><td>${new Date(st.at).toLocaleTimeString()}</td><td class="l" title="${esc(st.map)}">${esc(st.map)}</td><td class="num">${st.misses}/${st.clicks}</td><td class="num">${mv.join(" · ")}</td><td class="num">${f(b.w)} × ${f(b.h, 3)}</td></tr>${st.why ? `<tr class="why"><td></td><td class="l" colspan="4">${esc(st.why)}</td></tr>` : ""}`;
  }).join("");
  const start = L.start;
  // the stretch being played and when the next change comes
  let block = "";
  if (L.on && L.blockNeed > 0) {
    const done = Math.min(L.blockDone, L.blockNeed);
    const left = Math.max(0, Math.ceil(L.blockNeed - L.blockDone));
    block = `<div class="block"><div class="blockbar"><span style="width:${(100 * done) / L.blockNeed}%"></span></div>
      <span class="muted">${L.trying ? "Trying a nearby width to measure it. " : ""}${left > 0 ? `Next change after about ${left} more clicks, at the end of a map.` : "Next change at the end of this map (or in a pause)."}</span></div>`;
  }
  return `<div class="card">
    <div class="livebar">
      <button class="btn ${L.on ? "" : "primary"}" data-act="live" data-on="${L.on ? "0" : "1"}" ${pending ? "disabled" : ""}>${L.on ? "Stop" : "Start live calibration"}</button>
      <div class="status"><span class="pulse ${phase[0]}"></span><span>${phase[1]}</span></div>
      <span class="muted">${detail}</span>
    </div>
    ${block}
    <p class="sub" style="margin-top:14px">It plays in stretches of about ${Math.round(L.blockNeed || 150)} clicks: after each change it gives you time to get used to the new area (the first clicks count less), then, at the end of the map, it moves the area towards the one your clicks fit, with a sound: lower when it grows, higher when it shrinks. It remembers how you play at each width, day after day; while it isn't sure yet, now and then it plays a stretch at a nearby width to measure it. When nothing asks clearly for a change, the area stays. Any area you set by hand is where it starts from.</p>
    ${L.on && L.keys && L.keys.length ? `<div class="note">Clicks read from: ${L.keys.map(esc).join(", ")} <button class="link" data-goto="settings">change</button></div>` : ""}
    ${L.error ? `<div class="note warn">${esc(L.error)}</div>` : ""}
    <div class="caution"><b>For practice: offline or unranked.</b> Live changes the area while you play, from how you are playing: use it to practise, then play ranked with the best area it found and Live off. It doesn't touch osu!: it reads the window title, the cursor and the keys that click, like a key overlay, and changes the area in OpenTabletDriver.</div>
  </div>
  <div class="grid">
    ${bestCard()}
    <div class="card"><h2>Area now</h2>
      ${S.area ? `<div class="advice num" style="margin-top:0">${f(S.area.w)} × ${f(S.area.h, 3)} mm</div><div class="sub num">X ${f(S.area.x)} · Y ${f(S.area.y)}</div>` : `<div class="sub">OpenTabletDriver doesn't answer.</div>`}
      ${S.area ? tabletFig([{ r: S.area, kind: "now" }, ...(S.best ? [{ r: S.best, kind: "new" }] : [])]) : ""}
      <div class="legend"><span><span class="sw" style="border:1.5px dashed var(--muted)"></span>Now</span>${S.best ? `<span><span class="sw" style="background:var(--accent-soft);border:1.5px solid var(--accent)"></span>Best so far</span>` : ""}</div>
      ${start && S.area && !sameArea(start, S.area) ? `<div class="note">When you started: ${areaTxt(start)}</div><div class="btns"><button class="btn" data-act="live-back">Back to that area</button></div>` : ""}
    </div>
  </div>
  ${memoryCard()}
  <div class="card"><h2>Changes</h2>${stepRows ? `<table class="steps"><thead><tr><th>Time</th><th class="l">Map</th><th>Outside</th><th>Change (mm)</th><th>Area</th></tr></thead><tbody>${stepRows}</tbody></table>` : `<div class="sub">None yet.</div>`}</div>`;
}

// the best area so far, how reliable it is, and a button to keep it
function bestCard() {
  const rel = Math.max(0, Math.min(1, S.reliability || 0));
  const pct = Math.round(rel * 100);
  const label = rel >= 0.75 ? "Reliable: you can stop calibrating and keep it."
    : rel >= 0.4 ? "Getting there: a few more maps make it surer."
    : "Learning: play some maps with Live on.";
  const b = S.best;
  return `<div class="card"><h2>Best area so far</h2>
    ${b ? `<div class="advice num" style="margin-top:0">${f(b.w)} × ${f(b.h, 3)} mm</div><div class="sub num">X ${f(b.x)} · Y ${f(b.y)}</div>`
      : `<div class="sub">Not yet: it needs a few maps at a couple of widths.</div>`}
    ${b && S.memory && S.memory.hasBand ? `<div class="note">Inside your band (${trim(S.memory.bandLo, 1)}–${trim(S.memory.bandHi, 1)} mm) all widths go about as well: this is the one ${Math.abs(b.w - S.memory.best) < 0.6 ? "where you press closest to the centres" : "closest to where you play"}.</div>` : ""}
    <div class="relhead"><span>Reliability</span><b class="num">${pct}%</b></div>
    <div class="relbar${rel >= 0.75 ? " ok" : ""}"><span style="width:${pct}%"></span><i style="left:75%"></i></div>
    <div class="note">${label} It grows with the clicks around the best width and on both sides of it.</div>
    ${S.relDown ? `<div class="note warn">It went down from ${Math.round(S.relDown * 100)}%: a width with fewer clicks now looks better than the old best, and it's checking it. It grows again as you play.</div>` : ""}
    ${b ? `<div class="btns"><button class="btn${rel >= 0.75 ? " primary" : ""}" data-act="use-best" ${pending || (S.area && sameArea(S.area, b)) ? "disabled" : ""}>${S.area && sameArea(S.area, b) ? "In use" : S.live.on ? "Use it and stop Live" : "Use the best area"}</button></div>` : ""}
  </div>`;
}

let forgetArmed = false;
function memoryCard() {
  const M = S.memory;
  const lines = [];
  if (!M || !(M.clicks > 0)) {
    lines.push("Nothing yet. As you play with Live on, or finish maps with the program open, it learns how you do at each width.");
  } else {
    const n = `${Math.round(M.clicks).toLocaleString()} clicks${M.days > 1 ? ` over ${M.days} days` : ""}`;
    if (!M.hasBand) lines.push(`Learning (${n}). Until it has a few hundred clicks from a few maps at a couple of widths, it follows your clicks.`);
    else {
      let t = `<b>Your band: ${trim(M.bandLo, 1)}–${trim(M.bandHi, 1)} mm</b>, all about as good; closest to the centres around ${trim(M.best, 1)} mm (${n}).`;
      if (M.loWall) t += ` Below ${trim(M.bandLo, 1)} you press further from the centres.`;
      if (M.hiWall) t += ` Above ${trim(M.bandHi, 1)} you press further from the centres.`;
      lines.push(t);
    }
    if (M.hasCross) lines.push(`You land long below about ${trim(M.cross, 1)} mm and short above it.`);
    if (M.habit) lines.push(`At every width you land about ${Math.round(Math.abs(M.habitK) * 100)}% ${M.habitK < 0 ? "short" : "long"}: that's how you aim, not the area, so it doesn't ${M.habitK < 0 ? "shrink" : "grow"} the area for it.`);
  }
  const shown = (M && M.widths ? M.widths : []).filter((w) => w.clicks >= 15);
  // distance from the centres against the width nearest the best (or,
  // before there is one, the closest); "same" within the noise, as the band
  // judges it
  const nearest = M && M.hasBest ? shown.filter((w) => w.off > 0).sort((p, q) => Math.abs(p.w + 0.5 - M.best) - Math.abs(q.w + 0.5 - M.best))[0] : null;
  let ref = nearest ? nearest.off : 0, refSE = nearest ? nearest.offSE || 0 : 0;
  if (!(ref > 0)) ref = Math.min(...shown.filter((w) => w.off > 0).map((w) => w.off));
  const rows = shown.map((w) => {
    const land = w.jumps >= 40 ? (Math.abs(w.land) < 0.01 ? "on target" : `${Math.round(Math.abs(w.land) * 100)}% ${w.land < 0 ? "short" : "long"}`) : "–";
    const ok = w.off > 0 && isFinite(ref) && ref > 0;
    const d = ok ? Math.round((w.off / ref - 1) * 100) : null;
    const same = ok && Math.abs(w.off - ref) <= Math.max(0.04 * ref, 2 * Math.hypot(w.offSE || 0, refSE));
    const isBest = w === nearest;
    const miss = !ok ? "–" : isBest ? "best" : same || d === 0 ? "same" : `${d > 0 ? "+" : "−"}${Math.abs(d)}%`;
    const tag = w.band ? `<span class="tag">band</span>` : w.worse ? `<span class="tag bad">further off</span>` : "";
    const here = S.area && S.area.w >= w.w && S.area.w < w.w + 1;
    return `<tr${here ? ' class="here"' : ""}><td class="num">${w.w}–${w.w + 1}${here ? " ◂" : ""}</td><td class="num">${Math.round(w.clicks).toLocaleString()}</td><td class="num">${land}</td><td class="num">${miss}</td><td>${tag}</td></tr>`;
  }).join("");
  return `<div class="card"><h2>Your widths</h2>${lines.map((l) => `<p class="sub" style="margin:0 0 8px">${l}</p>`).join("")}
    ${rows ? `<table class="steps"><thead><tr><th>Width (mm)</th><th>Clicks</th><th>Lands on jumps</th><th>Off centre vs the best</th><th></th></tr></thead><tbody>${rows}</tbody></table>
    <div class="note">"Off centre" is how far from the centre of the circles you press (a press far off counts as one and a half radii, no more, so a hard map full of misses doesn't outweigh everything), compared on notes alike (same kind of jump, same time for it); "same" is a difference too small for the clicks so far to tell apart. Old days count less and less.</div>` : ""}
    ${M && M.clicks > 0 ? `<div class="btns"><button class="link" data-act="forget">${forgetArmed ? "Sure? Click again to forget it all" : "Forget it and start again"}</button></div>` : ""}
  </div>`;
}

function viewSettings() {
  const st = S.settings;
  const screenRatio = S.display && S.display.w > 0 ? S.display.h / S.display.w : 0;
  const nowRatio = S.area ? S.area.h / S.area.w : 0;
  const opt = (v, cur, label) => `<option value="${v}" ${v === cur ? "selected" : ""}>${label}</option>`;
  return `<div class="card"><h2>Area</h2><div class="form">
    <label for="ratio">Height / width</label>
    <div><div class="row"><input class="input short" id="ratio" data-set="ratio" inputmode="decimal" value="${st.ratio > 0 ? trim(st.ratio, 4) : ""}" placeholder="${nowRatio ? trim(nowRatio, 4) : ""}">
      ${screenRatio ? `<button class="link" data-ratio="${screenRatio}">like the screen (${trim(screenRatio, 4)})</button>` : ""}
      ${nowRatio ? `<button class="link" data-ratio="${nowRatio}">like the area now (${trim(nowRatio, 4)})</button>` : ""}</div>
      <div class="help">The shape stays fixed: the advice changes only size and position. 0.5625 is 16:9, 0.75 is 4:3. Empty: the shape of the area in use.</div></div>
    <label for="minW">Width range (mm)</label>
    <div><div class="row"><input class="input short" id="minW" data-set="minW" inputmode="decimal" value="${st.minW > 0 ? trim(st.minW, 2) : ""}" placeholder="no min"> – <input class="input short" id="maxW" data-set="maxW" inputmode="decimal" value="${st.maxW > 0 ? trim(st.maxW, 2) : ""}" placeholder="no max"></div>
      <div class="help">Optional. The live calibration finds the widths that suit you by itself (see Live): a range here is a hard wall for it and for the advice.</div></div>
  </div></div>
  <div class="card"><h2>Live</h2><div class="form">
    <label for="keys">Keys that click</label>
    <div><input class="input" id="keys" data-set="keys" value="${esc(st.keys)}" placeholder="from osu! (for example Z X)">
      <div class="help">osu! names, separated by spaces: Z, X, V, B, D1, LeftShift, Space… Empty: the ones in osu! stable's settings (Z X for lazer).</div></div>
    <label for="penTip">Pen tip clicks</label>
    <div><select class="input short" id="penTip" data-set="penTip" style="width:220px">${opt("auto", st.penTip, "as osu! says")}${opt("on", st.penTip, "yes")}${opt("off", st.penTip, "no")}</select></div>
    <label for="strength">Each change</label>
    <div><select class="input" id="strength" data-set="strength" style="width:220px">${opt("gentle", st.strength, "gentle")}${opt("normal", st.strength, "normal")}${opt("strong", st.strength, "strong")}</select>
      <div class="help">How often and how far: gentle changes after about 200 clicks by at most 0.8% of the width and 0.5 mm, normal after 150 by 1.5% and 0.8 mm, strong after 100 by 2.5% and 1.2 mm.</div></div>
    <label for="tryWidths">Try nearby widths</label>
    <div><select class="input" id="tryWidths" data-set="tryWidths" style="width:220px">${opt("1", st.tryWidths ? "1" : "0", "yes")}${opt("0", st.tryWidths ? "1" : "0", "no")}</select>
      <div class="help">While it isn't sure of the best width, now and then it plays a stretch about 2 mm narrower or wider (or at a width that might be better) to measure it, then comes back. Without it, it only learns the widths it passes through.</div></div>
    <label for="sound">Sound</label>
    <div class="row"><select class="input" id="sound" data-set="sound" style="width:220px">${opt("1", st.sound ? "1" : "0", "on")}${opt("0", st.sound ? "1" : "0", "off")}</select><button class="link" data-act="sound">play it</button></div>
  </div></div>
  <div class="card"><h2>Where things are</h2><div class="form">
    <label for="osuDir">osu! stable folder</label>
    <div><input class="input" id="osuDir" data-set="osuDir" value="${esc(st.osuDir)}" placeholder="${esc(S.osuDir || "not found")}"></div>
    <label for="lazerDir">osu!lazer folder</label>
    <div><input class="input" id="lazerDir" data-set="lazerDir" value="${esc(st.lazerDir)}" placeholder="${esc(S.lazerData || "not found")}"></div>
    <label for="otdPath">OpenTabletDriver settings.json</label>
    <div><input class="input" id="otdPath" data-set="otdPath" value="${esc(st.otdPath)}" placeholder="${esc(S.otdPath || "not found")}"></div>
    <label for="tablet">Tablet</label>
    <div><input class="input" id="tablet" data-set="tablet" value="${esc(st.tablet)}" placeholder="${esc(S.tablet || "the connected one")}">
      <div class="help">${S.tablet ? `Using ${esc(S.tablet)}${S.tabletW ? ` (${trim(S.tabletW, 2)} × ${trim(S.tabletH, 2)} mm)` : ""}.` : ""} Empty: the one connected.</div></div>
    <label></label><div><button class="link" data-act="open-data">Open the data folder (log)</button></div>
  </div></div>`;
}

// what each section shows: it is drawn again only when that changes, so
// that a button doesn't move under the pointer for nothing
function viewKey(t) {
  const pick = t === "live" ? [S.live, S.area, pending, S.memory, forgetArmed, S.best, S.reliability, S.relDown]
    : t === "settings" ? [S.settings, S.display, S.area && S.area.h / S.area.w, S.osuDir, S.lazerData, S.otdPath, S.tablet, S.tabletW]
    : [S.report, S.busy, S.replayMsg, S.area, S.undo, S.note, S.lazerScan, S.tabletW, pending, S.report && ago(S.report.playedAt), S.memory && [S.memory.hasBand, S.memory.bandLo, S.memory.bandHi]];
  return t + JSON.stringify(pick);
}
let drawn = "";

function render() {
  if (!S) return;
  document.getElementById("ver").textContent = S.version;
  document.querySelectorAll(".tab").forEach((b) => b.classList.toggle("on", b.dataset.tab === tab));
  document.getElementById("areaNow").innerHTML = S.area
    ? `<b class="num">${f(S.area.w)} × ${f(S.area.h, 3)} mm</b><br><span class="num">X ${f(S.area.x)} · Y ${f(S.area.y)}</span>`
    : `<span>${esc(S.areaErr || "reading the area…")}</span>`;
  document.getElementById("problems").innerHTML = (S.problems || []).map((p) => `<div class="problem">${esc(p)}</div>`).join("");
  const view = document.getElementById("view");
  if (tab === "settings" && view.dataset.tab === "settings" && view.contains(document.activeElement) && document.activeElement.matches("input,select")) return;
  const key = viewKey(tab);
  if (key === drawn && view.dataset.tab === tab) return;
  drawn = key;
  view.dataset.tab = tab;
  view.innerHTML = tab === "live" ? viewLive() : tab === "settings" ? viewSettings() : viewReplay();
}

function setState(st, force) {
  const j = JSON.stringify(st);
  if (!force && j === lastJSON) return;
  lastJSON = j;
  S = st;
  render();
}

// ---------- events ----------
document.addEventListener("click", (e) => {
  const t = e.target.closest("[data-tab],[data-act],[data-goto],[data-ratio]");
  if (!t) return;
  if (t.dataset.tab || t.dataset.goto) {
    tab = t.dataset.tab || t.dataset.goto;
    try { localStorage.setItem("tab", tab); } catch (err) { /* no storage */ }
    render();
    return;
  }
  if (t.dataset.ratio) {
    saveSetting("ratio", Number(Number(t.dataset.ratio).toFixed(4)));
    return;
  }
  switch (t.dataset.act) {
    case "analyze": act("analyze"); break;
    case "apply": act("apply", { id: Number(t.dataset.id) }, "Area set in OpenTabletDriver"); break;
    case "undo": act("undo", {}, "Earlier area back"); break;
    case "reopen": act("reopen-otd", {}, "OpenTabletDriver window reopened"); break;
    case "live": act("live", { on: t.dataset.on === "1" }); break;
    case "live-back": act("live-back", {}, "Starting area back"); break;
    case "use-best": act("use-best", {}, "Best area set in OpenTabletDriver"); break;
    case "forget":
      if (!forgetArmed) { forgetArmed = true; drawn = ""; render(); setTimeout(() => { forgetArmed = false; drawn = ""; render(); }, 4000); }
      else { forgetArmed = false; act("forget", {}, "Forgotten: it starts learning again"); }
      break;
    case "sound": post("sound").catch((err) => toast(err.message, true)); break;
    case "open-data": post("open-data").catch((err) => toast(err.message, true)); break;
  }
});
async function saveSetting(key, value) {
  try {
    const st = await post("settings", { [key]: value });
    setState(st, true);
    if (tab === "settings") { drawn = ""; render(); }
    toast("Saved");
  } catch (err) {
    toast(err.message, true);
  }
}
document.addEventListener("change", (e) => {
  const el = e.target.closest("[data-set]");
  if (!el) return;
  const k = el.dataset.set;
  let v = el.value.trim();
  if (["ratio", "minW", "maxW"].includes(k)) {
    v = v.replace(",", ".");
    if (v === "") v = 0;
    else if (!isFinite(Number(v))) { toast("Type a number", true); return; }
    else v = Number(v);
  }
  if (k === "sound" || k === "tryWidths") v = v === "1";
  el.blur();
  saveSetting(k, v);
});

async function poll() {
  try {
    setState(await getState());
  } catch (e) {
    document.getElementById("areaNow").textContent = e.message;
  }
}
poll();
setInterval(poll, 1000);
