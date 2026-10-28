/* Small SVG chart kit for Area Coach. Charts are drawn at the container's
   real pixel width (text never scales) and redrawn on resize. */
(function () {
  "use strict";
  const NF = (d) => new Intl.NumberFormat("en-US", { minimumFractionDigits: d, maximumFractionDigits: d });
  const fmt = (v, d = 1) => (v == null || !isFinite(v) ? "–" : NF(d).format(v));
  const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
  const tip = () => document.getElementById("tip");

  function niceTicks(lo, hi, n = 5) {
    if (!(hi > lo)) { hi = lo + 1; }
    const span = hi - lo;
    const step0 = span / n;
    const mag = Math.pow(10, Math.floor(Math.log10(step0)));
    const r = step0 / mag;
    const step = (r < 1.5 ? 1 : r < 3 ? 2 : r < 7 ? 5 : 10) * mag;
    const out = [];
    for (let v = Math.ceil(lo / step - 1e-9) * step; v <= hi + step * 1e-6; v += step) out.push(+v.toFixed(10));
    return { ticks: out, step };
  }
  function decimalsFor(step) { return step >= 1 ? 0 : Math.min(3, Math.ceil(-Math.log10(step) - 1e-9)); }

  const registry = new Map();
  const ro = typeof ResizeObserver !== "undefined" ? new ResizeObserver((entries) => {
    for (const e of entries) {
      const r = registry.get(e.target);
      if (r && Math.abs(e.contentRect.width - r.w) > 2) draw(e.target, r.fn, r.opts);
    }
  }) : null;

  function draw(el, fn, opts) {
    if (!el) return;
    const w = Math.max(240, Math.floor(el.clientWidth || 600));
    const res = fn(w, opts);
    el.innerHTML = res.svg;
    registry.set(el, { fn, opts, w });
    if (ro) ro.observe(el);
    if (res.bind) res.bind(el);
  }

  function showTip(html, ev) {
    const t = tip();
    t.innerHTML = html;
    t.classList.add("on");
    const pad = 14;
    const r = t.getBoundingClientRect();
    let x = ev.clientX + pad;
    let y = ev.clientY + pad;
    if (x + r.width > window.innerWidth - 8) x = ev.clientX - r.width - pad;
    if (y + r.height > window.innerHeight - 8) y = ev.clientY - r.height - pad;
    t.style.left = x + "px";
    t.style.top = y + "px";
  }
  function hideTip() { tip().classList.remove("on"); }

  /* ---------------- line chart ---------------- */
  function line(w, o) {
    const h = o.height || 260;
    const m = { l: o.ml || 48, r: o.mr || 16, t: 14, b: o.xLabel ? 42 : 28 };
    const xs = o.x;
    const n = xs.length;
    let ymin = Infinity, ymax = -Infinity;
    for (const s of o.series) {
      for (const arr of [s.values, s.lo, s.hi]) {
        if (!arr) continue;
        for (const v of arr) if (v != null && isFinite(v) && v >= (o.validMin ?? -Infinity)) { ymin = Math.min(ymin, v); ymax = Math.max(ymax, v); }
      }
    }
    for (const pm of o.points || []) if (pm.y != null && isFinite(pm.y)) { ymin = Math.min(ymin, pm.y); ymax = Math.max(ymax, pm.y); }
    if (o.yMin != null) ymin = o.yMin;
    if (o.yMax != null) ymax = o.yMax;
    if (!isFinite(ymin)) { ymin = 0; ymax = 1; }
    if (ymax - ymin < (o.minSpan || 1e-6)) { const c = (ymax + ymin) / 2; ymin = c - (o.minSpan || 1) / 2; ymax = c + (o.minSpan || 1) / 2; }
    const padY = (ymax - ymin) * 0.08;
    if (o.yMin == null) ymin -= padY;
    if (o.yMax == null) ymax += padY;
    const { ticks: yt, step: ys } = niceTicks(ymin, ymax, o.yTicks || 4);
    const x0 = o.xMin ?? xs[0], x1 = o.xMax ?? xs[n - 1];
    const X = (v) => m.l + ((v - x0) / (x1 - x0 || 1)) * (w - m.l - m.r);
    const Y = (v) => m.t + (1 - (v - ymin) / (ymax - ymin)) * (h - m.t - m.b);
    const yFmt = o.yFmt || ((v) => fmt(v, decimalsFor(ys)));
    const xFmt = o.xFmt || ((v) => fmt(v, 0));
    let s = `<svg viewBox="0 0 ${w} ${h}" width="${w}" height="${h}" role="img" aria-label="${esc(o.aria || "chart")}">`;
    // shading
    for (const sh of o.shade || []) {
      const a = X(Math.max(sh.x0, x0)), b = X(Math.min(sh.x1, x1));
      if (b > a && sh.fill !== false) s += `<rect x="${a}" y="${m.t}" width="${b - a}" height="${h - m.t - m.b}" style="fill:var(--line-soft);opacity:.55"/>`;
      if (sh.label && b - a > 44) s += `<text x="${(a + b) / 2}" y="${m.t + 13}" text-anchor="middle"${sh.strong ? ' class="lbl-strong"' : ""}>${esc(sh.label)}</text>`;
    }
    s += `<g class="grid">`;
    for (const v of yt) { if (v < ymin - 1e-9 || v > ymax + 1e-9) continue; s += `<line x1="${m.l}" x2="${w - m.r}" y1="${Y(v)}" y2="${Y(v)}"/>`; }
    s += `</g>`;
    for (const v of yt) { if (v < ymin - 1e-9 || v > ymax + 1e-9) continue; s += `<text x="${m.l - 8}" y="${Y(v) + 4}" text-anchor="end">${esc(yFmt(v))}</text>`; }
    // x ticks
    const xticks = o.xTicks || niceTicks(x0, x1, Math.max(3, Math.floor((w - m.l - m.r) / 90))).ticks;
    for (const v of xticks) { if (v < x0 - 1e-9 || v > x1 + 1e-9) continue; s += `<text x="${X(v)}" y="${h - m.b + 18}" text-anchor="middle">${esc(xFmt(v))}</text>`; }
    s += `<line class="baseline" x1="${m.l}" x2="${w - m.r}" y1="${h - m.b}" y2="${h - m.b}"/>`;
    if (o.xLabel) s += `<text x="${(m.l + w - m.r) / 2}" y="${h - 6}" text-anchor="middle">${esc(o.xLabel)}</text>`;
    if (o.yLabel) s += `<text x="${m.l}" y="${m.t - 2}" text-anchor="start" class="lbl-strong" style="font-weight:500">${esc(o.yLabel)}</text>`;
    // zero line
    if (o.zero && ymin < 0 && ymax > 0) s += `<line x1="${m.l}" x2="${w - m.r}" y1="${Y(0)}" y2="${Y(0)}" style="stroke:var(--text-3);stroke-dasharray:3 3"/>`;
    // bands
    for (const se of o.series) {
      if (!se.lo || !se.hi) continue;
      if (se.bars) {
        // error bars instead of a band
        for (let i = 0; i < n; i++) {
          const lo = se.lo[i], hi = se.hi[i];
          if (lo == null || hi == null || !isFinite(lo) || !isFinite(hi) || hi - lo < 1e-9) continue;
          const xv = X(xs[i]);
          s += `<path d="M${xv},${Y(lo)}V${Y(hi)}M${xv - 4},${Y(lo)}h8M${xv - 4},${Y(hi)}h8" style="fill:none;stroke:${se.color};stroke-width:1.5;opacity:.7"/>`;
        }
        continue;
      }
      let up = "", dn = "";
      let started = false;
      for (let i = 0; i < n; i++) {
        const lo = se.lo[i], hi = se.hi[i];
        if (lo == null || hi == null || !isFinite(lo) || !isFinite(hi) || lo < (o.validMin ?? -Infinity)) continue;
        up += `${started ? "L" : "M"}${X(xs[i])},${Y(hi)}`;
        dn = `L${X(xs[i])},${Y(lo)}` + dn;
        started = true;
      }
      if (started) s += `<path d="${up}${dn}Z" style="fill:${se.color};opacity:.14"/>`;
    }
    // vertical markers
    for (const vm of o.vmarks || []) {
      if (vm.x < x0 || vm.x > x1) continue;
      const xv = X(vm.x);
      s += `<line x1="${xv}" x2="${xv}" y1="${m.t}" y2="${h - m.b}" style="stroke:${vm.color};stroke-width:${vm.width || 1.5};${vm.dash ? "stroke-dasharray:4 4" : ""}"/>`;
      if (vm.label) {
        let anchor = xv > w - 110 ? "end" : xv < m.l + 80 ? "start" : "middle";
        if (vm.side === "right") anchor = xv > w - 140 ? "end" : "start";
        s += `<text x="${xv + (anchor === "start" ? 4 : anchor === "end" ? -4 : 0)}" y="${m.t + 12 + (vm.row || 0) * 14}" text-anchor="${anchor}" style="fill:${vm.color};font-weight:600">${esc(vm.label)}</text>`;
      }
    }
    // lines
    for (const se of o.series) {
      let d = "";
      let pen = false;
      for (let i = 0; i < n; i++) {
        const v = se.values[i];
        if (v == null || !isFinite(v) || v < (o.validMin ?? -Infinity)) { pen = false; continue; }
        d += `${pen ? "L" : "M"}${X(xs[i])},${Y(v)}`;
        pen = true;
      }
      s += `<path d="${d}" style="fill:none;stroke:${se.color};stroke-width:${se.width || 2};stroke-linejoin:round;stroke-linecap:round;${se.dash ? "stroke-dasharray:5 4" : ""}"/>`;
      if (se.dots) {
        for (let i = 0; i < n; i++) {
          const v = se.values[i];
          if (v == null || !isFinite(v)) continue;
          s += `<circle cx="${X(xs[i])}" cy="${Y(v)}" r="${se.dotR || 3.5}" style="fill:${se.color};stroke:var(--surface);stroke-width:2"/>`;
        }
      }
    }
    // point markers
    for (const pm of o.points || []) {
      if (pm.ring) s += `<circle cx="${X(pm.x)}" cy="${Y(pm.y)}" r="${(pm.r || 5) + 3}" style="fill:none;stroke:${pm.color};stroke-width:1.5"/>`;
      if (pm.hollow) s += `<circle cx="${X(pm.x)}" cy="${Y(pm.y)}" r="${pm.r || 5}" style="fill:var(--surface);stroke:${pm.color};stroke-width:2"/>`;
      else s += `<circle cx="${X(pm.x)}" cy="${Y(pm.y)}" r="${pm.r || 5}" style="fill:${pm.color};stroke:var(--surface);stroke-width:2"/>`;
    }
    // direct labels at line ends
    if (o.directLabels) {
      const placed = [];
      for (const se of o.series) {
        let i = n - 1;
        while (i >= 0 && (se.values[i] == null || !isFinite(se.values[i]))) i--;
        if (i < 0) continue;
        let y = Y(se.values[i]);
        for (const p of placed) if (Math.abs(p - y) < 13) y = p + 13;
        placed.push(y);
        s += `<text x="${X(xs[i]) + 6}" y="${y + 4}" style="fill:var(--text-2)">${esc(se.name)}</text>`;
      }
    }
    s += `<line class="xh" x1="0" x2="0" y1="${m.t}" y2="${h - m.b}" style="stroke:var(--text-3);opacity:0"/>`;
    s += `<rect class="hit" x="${m.l}" y="${m.t}" width="${w - m.l - m.r}" height="${h - m.t - m.b}" style="fill:transparent"/>`;
    s += `</svg>`;
    const bind = (el) => {
      const svg = el.querySelector("svg");
      const hit = svg.querySelector(".hit");
      const xh = svg.querySelector(".xh");
      hit.addEventListener("mousemove", (ev) => {
        const r = svg.getBoundingClientRect();
        const px = ((ev.clientX - r.left) / r.width) * w;
        const py = ((ev.clientY - r.top) / r.height) * h;
        // a point under the pointer wins over the curves
        let pb = null, pd = 12;
        for (const pm of o.points || []) {
          if (!pm.tip) continue;
          const dd = Math.hypot(X(pm.x) - px, Y(pm.y) - py);
          if (dd < pd) { pd = dd; pb = pm; }
        }
        if (pb) { xh.style.opacity = 0; showTip(pb.tip, ev); return; }
        if (!n) return;
        let bi = 0, bd = Infinity;
        for (let i = 0; i < n; i++) { const d = Math.abs(X(xs[i]) - px); if (d < bd) { bd = d; bi = i; } }
        xh.setAttribute("x1", X(xs[bi])); xh.setAttribute("x2", X(xs[bi])); xh.style.opacity = 0.6;
        let html = `<b>${esc(o.tipX ? o.tipX(xs[bi], bi) : xFmt(xs[bi]))}</b>`;
        for (const se of o.series) {
          const v = se.values[bi];
          if (v == null || !isFinite(v) || v < (o.validMin ?? -Infinity)) continue;
          html += `<div class="r"><span><i style="--c:${se.color}"></i>${esc(se.name)}</span><span>${esc((o.tipFmt || yFmt)(v, se, bi))}</span></div>`;
        }
        showTip(html, ev);
      });
      hit.addEventListener("mouseleave", () => { xh.style.opacity = 0; hideTip(); });
    };
    return { svg: s, bind };
  }

  /* ---------------- scatter on a unit circle ---------------- */
  function target(w, o) {
    const size = Math.min(w, o.height || 320);
    const h = size;
    const cx = w / 2, cy = h / 2;
    const lim = o.limit || 2;
    const k = (size / 2 - 18) / lim;
    let s = `<svg viewBox="0 0 ${w} ${h}" width="${w}" height="${h}" role="img" aria-label="${esc(o.aria || "click distribution")}">`;
    for (let r = 0.5; r <= lim; r += 0.5) {
      s += `<circle cx="${cx}" cy="${cy}" r="${r * k}" style="fill:${r === 1 ? "var(--now-fill)" : "none"};stroke:${r === 1 ? "var(--now)" : "var(--grid)"};stroke-width:${r === 1 ? 1.6 : 1}"/>`;
    }
    s += `<line x1="${cx - lim * k}" x2="${cx + lim * k}" y1="${cy}" y2="${cy}" style="stroke:var(--grid)"/>`;
    s += `<line y1="${cy - lim * k}" y2="${cy + lim * k}" x1="${cx}" x2="${cx}" style="stroke:var(--grid)"/>`;
    s += `<path d="M${cx - lim * k},${cy - lim * k + 10}h44 m-8,-6 l8,6 -8,6" style="fill:none;stroke:var(--text-3);stroke-width:1.5;stroke-linecap:round;stroke-linejoin:round"/>`;
    s += `<text x="${cx - lim * k}" y="${cy - lim * k - 2}">jump direction</text>`;
    for (const p of o.points) {
      const x = cx + p.x * k, y = cy + p.y * k;
      if (p.hit) s += `<circle cx="${x}" cy="${y}" r="2" style="fill:var(--c1);opacity:.55"/>`;
      else s += `<path d="M${x - 3},${y - 3}l6,6M${x + 3},${y - 3}l-6,6" style="stroke:var(--bad);stroke-width:1.6"/>`;
    }
    if (o.mean) s += `<circle cx="${cx + o.mean.x * k}" cy="${cy + o.mean.y * k}" r="5" style="fill:var(--next);stroke:var(--surface);stroke-width:2"/>`;
    s += `</svg>`;
    return { svg: s };
  }

  /* ---------------- tablet drawing ---------------- */
  const KIND = {
    now: { color: "var(--now)", fill: "var(--now-fill)", width: 2, dash: "" },
    next: { color: "var(--next)", fill: "none", width: 2.4, dash: "" },
    ideal: { color: "var(--next)", fill: "none", width: 1.6, dash: "6 4" },
    ratio: { color: "var(--alt)", fill: "none", width: 1.6, dash: "6 4" },
    shift: { color: "var(--c4)", fill: "none", width: 1.6, dash: "6 4" },
    comp: { color: "var(--next)", fill: "none", width: 2.4, dash: "" },
    past: { color: "var(--text-3)", fill: "none", width: 1, dash: "2 4" },
  };
  function corners(r) {
    const t = (r.rot || 0) * Math.PI / 180, c = Math.cos(t), sn = Math.sin(t);
    const hw = r.w / 2, hh = r.h / 2;
    return [[-hw, -hh], [hw, -hh], [hw, hh], [-hw, hh]].map(([x, y]) => [r.x + c * x - sn * y, r.y + sn * x + c * y]);
  }
  let clipSeq = 0;
  function tablet(w, o) {
    let tw = o.tw, th = o.th;
    let areas = (o.areas || []).map((a) => ({ ...a }));
    // exaggerate a small difference between the current area and the next step
    let gain = 1;
    const nowA = areas.find((a) => a.kind === "now"), nextA = areas.find((a) => a.kind === "next");
    const all0 = areas.map((a) => corners(a.rect)).flat();
    if (!(tw > 0 && th > 0)) {
      let mx = 0, my = 0;
      for (const [x, y] of all0) { mx = Math.max(mx, x); my = Math.max(my, y); }
      tw = Math.max(mx * 1.15, 100); th = Math.max(my * 1.15, 60);
    }
    // view window (mm)
    let vx0 = 0, vy0 = 0, vx1 = tw, vy1 = th;
    if (o.zoom && all0.length) {
      let bx0 = Infinity, by0 = Infinity, bx1 = -Infinity, by1 = -Infinity;
      for (const [x, y] of all0) { bx0 = Math.min(bx0, x); by0 = Math.min(by0, y); bx1 = Math.max(bx1, x); by1 = Math.max(by1, y); }
      const mg = Math.max(6, (bx1 - bx0) * 0.12);
      vx0 = Math.max(-4, bx0 - mg); vy0 = Math.max(-4, by0 - mg); vx1 = Math.min(tw + 4, bx1 + mg); vy1 = Math.min(th + 4, by1 + mg);
      const aspect = 0.62;
      if ((vy1 - vy0) < (vx1 - vx0) * aspect * 0.8) { const c = (vy0 + vy1) / 2, hh = (vx1 - vx0) * aspect * 0.4; vy0 = c - hh; vy1 = c + hh; }
    }
    const vw = vx1 - vx0, vh = vy1 - vy0;
    const pad = { l: 36, r: 14, t: 32, b: 16 };
    const maxH = o.maxHeight || 520;
    let k = (w - pad.l - pad.r) / vw;
    if (vh * k + pad.t + pad.b > maxH) k = (maxH - pad.t - pad.b) / vh;
    const h = Math.round(vh * k + pad.t + pad.b);
    const ox = pad.l + ((w - pad.l - pad.r) - vw * k) / 2, oy = pad.t;
    const P = (x, y) => [ox + (x - vx0) * k, oy + (y - vy0) * k];
    if (o.exaggerate && nowA && nextA) {
      const a = corners(nowA.rect), b = corners(nextA.rect);
      let md = 0;
      for (let i = 0; i < 4; i++) md = Math.max(md, Math.hypot(a[i][0] - b[i][0], a[i][1] - b[i][1]) * k);
      if (md > 0.01 && md < 16) {
        gain = Math.min(10, Math.ceil(16 / md));
        const r0 = nowA.rect, r1 = nextA.rect;
        nextA.rect = { w: r0.w + (r1.w - r0.w) * gain, h: r0.h + (r1.h - r0.h) * gain, x: r0.x + (r1.x - r0.x) * gain, y: r0.y + (r1.y - r0.y) * gain, rot: (r0.rot || 0) + ((r1.rot || 0) - (r0.rot || 0)) * gain };
        if (gain > 1) nextA.label = (nextA.label || "") + ` (difference ×${gain})`;
      }
    }
    const cid = "tclip" + (++clipSeq);
    let s = `<svg viewBox="0 0 ${w} ${h}" width="${w}" height="${h}" role="img" aria-label="${esc(o.aria || "tablet with the areas")}">`;
    s += `<defs><clipPath id="${cid}"><rect x="${ox - 12}" y="${oy - 12}" width="${vw * k + 24}" height="${vh * k + 24}" rx="14"/></clipPath></defs>`;
    const [tx0, ty0] = P(0, 0), [tx1, ty1] = P(tw, th);
    s += `<g clip-path="url(#${cid})"><rect x="${tx0 - 10}" y="${ty0 - 10}" width="${tx1 - tx0 + 20}" height="${ty1 - ty0 + 20}" rx="14" style="fill:var(--tablet);stroke:var(--tablet-edge);stroke-width:1"/>`;
    if (o.zoom) s += `<rect x="${tx0}" y="${ty0}" width="${tx1 - tx0}" height="${ty1 - ty0}" style="fill:none;stroke:var(--tablet-edge);stroke-dasharray:2 3"/>`;
    // rulers
    const span = vw;
    const step = span > 180 ? 10 : span > 70 ? 5 : span > 30 ? 2 : 1;
    const major = step * (span > 70 ? 4 : 5);
    const rx0 = Math.ceil(vx0 / step) * step, ry0 = Math.ceil(vy0 / step) * step;
    let rul = "";
    for (let x = rx0; x <= vx1 + 1e-6; x += step) {
      const [px] = P(x, 0);
      const mj = Math.abs(Math.round(x / major) * major - x) < 1e-6;
      rul += `<line x1="${px}" x2="${px}" y1="${oy - 12 - (mj ? 9 : 5)}" y2="${oy - 12}" style="stroke:var(--text-3);stroke-width:1;opacity:${mj ? 0.9 : 0.5}"/>`;
      if (mj) rul += `<text x="${px}" y="${oy - 25}" text-anchor="middle" style="font-size:10.5px">${Math.round(x)}</text>`;
    }
    for (let y = ry0; y <= vy1 + 1e-6; y += step) {
      const [, py] = P(0, y);
      const mj = Math.abs(Math.round(y / major) * major - y) < 1e-6;
      rul += `<line y1="${py}" y2="${py}" x1="${ox - 12 - (mj ? 9 : 5)}" x2="${ox - 12}" style="stroke:var(--text-3);stroke-width:1;opacity:${mj ? 0.9 : 0.5}"/>`;
      if (mj) rul += `<text y="${py + 4}" x="${ox - 24}" text-anchor="end" style="font-size:10.5px">${Math.round(y)}</text>`;
    }
    // playfield
    if (o.playfield) {
      const d = o.playfield.map(([x, y], i) => (i ? "L" : "M") + P(x, y).join(",")).join("") + "Z";
      s += `<path d="${d}" style="fill:none;stroke:var(--text-3);stroke-width:1;stroke-dasharray:1.5 3"/>`;
    }
    // field arrows
    if (o.field && o.field.length) {
      const fs = o.fieldScale || 4;
      o.field.forEach((c, i) => {
        const [x1, y1] = P(c.x, c.y);
        const x2 = x1 + c.ex * k * fs, y2 = y1 + c.ey * k * fs;
        const len = Math.hypot(x2 - x1, y2 - y1);
        s += `<circle cx="${x1}" cy="${y1}" r="1.8" style="fill:var(--text-3)"/>`;
        if (len > 1.5) {
          const a = Math.atan2(y2 - y1, x2 - x1);
          const ah = Math.min(6, len * 0.45);
          s += `<path d="M${x1},${y1}L${x2},${y2}M${x2},${y2}L${x2 - ah * Math.cos(a - 0.45)},${y2 - ah * Math.sin(a - 0.45)}M${x2},${y2}L${x2 - ah * Math.cos(a + 0.45)},${y2 - ah * Math.sin(a + 0.45)}" style="stroke:var(--c2);stroke-width:1.6;fill:none;stroke-linecap:round"/>`;
        }
        s += `<circle class="cell" data-i="${i}" cx="${x1}" cy="${y1}" r="${Math.max(8, 2.5 * k)}" style="fill:transparent"/>`;
      });
    }
    // where the centre of the area went, stretch by stretch
    if (o.trail && o.trail.length > 1) {
      const pts = o.trail.map(([x, y]) => P(x, y));
      s += `<path d="${pts.map((p, i) => (i ? "L" : "M") + p.join(",")).join("")}" style="fill:none;stroke:var(--text-3);stroke-width:1;opacity:.55;stroke-linejoin:round"/>`;
      pts.forEach((p, i) => { s += `<circle cx="${p[0]}" cy="${p[1]}" r="${i === pts.length - 1 ? 2.6 : 1.5}" style="fill:var(--text-3);opacity:${i === pts.length - 1 ? 0.9 : 0.5}"/>`; });
    }
    // areas
    const labels = [];
    for (const a of areas) {
      const kd = KIND[a.kind] || KIND.past;
      const pts = corners(a.rect).map(([x, y]) => P(x, y));
      const d = pts.map((p, i) => (i ? "L" : "M") + p.join(",")).join("") + "Z";
      s += `<path d="${d}" style="fill:${kd.fill};stroke:${kd.color};stroke-width:${kd.width};${kd.dash ? `stroke-dasharray:${kd.dash}` : ""};stroke-linejoin:round"/>`;
      if (a.kind === "now" || a.kind === "next" || a.kind === "comp") {
        // corner brackets, like the marks on a real tablet
        const L = Math.min(16, Math.hypot(pts[1][0] - pts[0][0], pts[1][1] - pts[0][1]) * 0.16);
        for (let i = 0; i < 4; i++) {
          const p = pts[i], pa = pts[(i + 3) % 4], pb = pts[(i + 1) % 4];
          const ua = [pa[0] - p[0], pa[1] - p[1]], ub = [pb[0] - p[0], pb[1] - p[1]];
          const la = Math.hypot(...ua) || 1, lb = Math.hypot(...ub) || 1;
          s += `<path d="M${p[0] + ua[0] / la * L},${p[1] + ua[1] / la * L}L${p[0]},${p[1]}L${p[0] + ub[0] / lb * L},${p[1] + ub[1] / lb * L}" style="fill:none;stroke:${kd.color};stroke-width:${kd.width + 1.6};stroke-linecap:round;stroke-linejoin:round"/>`;
        }
      }
      if (a.center) {
        const [cx, cy] = P(a.rect.x, a.rect.y);
        s += `<path d="M${cx - 5},${cy}h10M${cx},${cy - 5}v10" style="stroke:${kd.color};stroke-width:1.4"/>`;
      }
      if (a.label) labels.push({ pts, text: a.label, color: kd.color, below: a.kind === "now" });
    }
    // labels: current area below its bottom edge, others above the top edge
    for (const lb of labels) {
      const xs = lb.pts.map((p) => p[0]), ys = lb.pts.map((p) => p[1]);
      const x = Math.min(...xs);
      const y = lb.below ? Math.max(...ys) + 17 : Math.min(...ys) - 9;
      s += `<text x="${x}" y="${y}" style="fill:${lb.color};font-weight:600;font-size:12px;paint-order:stroke;stroke:var(--tablet);stroke-width:4px">${esc(lb.text)}</text>`;
    }
    s += `</g>${rul}`;
    if (o.tabletName && !o.zoom) s += `<text x="${tx1}" y="${ty1 - 8}" text-anchor="end" style="font-size:11px;opacity:.8">${esc(o.tabletName)}</text>`;
    s += `<text x="${ox + vw * k + 10}" y="${oy - 25}" text-anchor="end" style="font-size:10.5px">mm</text>`;
    s += `</svg>`;
    const bind = o.field ? (el) => {
      el.querySelectorAll(".cell").forEach((c) => {
        c.addEventListener("mousemove", (ev) => {
          const f = o.field[+c.dataset.i];
          showTip(`<b>Zone ${fmt(f.x, 0)}, ${fmt(f.y, 0)} mm</b><div class="r"><span>average error</span><span>${fmt(Math.hypot(f.ex, f.ey), 2)} mm</span></div><div class="r"><span>horizontal</span><span>${fmt(f.ex, 2)} mm</span></div><div class="r"><span>vertical</span><span>${fmt(f.ey, 2)} mm</span></div><div class="r"><span>notes</span><span>${f.n}</span></div>`, ev);
        });
        c.addEventListener("mouseleave", hideTip);
      });
    } : null;
    return { svg: s, bind, gain };
  }

  /* ---------------- polar gain by direction ---------------- */
  function polar(w, o) {
    const size = Math.min(w, o.height || 300);
    const h = size, cx = w / 2, cy = h / 2;
    const R = size / 2 - 34;
    const lim = Math.max(4, Math.ceil(Math.max(...o.points.map((p) => Math.abs(p.gain)), 1) / 2) * 2);
    const rad = (g) => R * (0.5 + 0.5 * g / lim);
    let s = `<svg viewBox="0 0 ${w} ${h}" width="${w}" height="${h}" role="img" aria-label="error by direction">`;
    for (const g of [-lim, -lim / 2, 0, lim / 2, lim]) {
      s += `<circle cx="${cx}" cy="${cy}" r="${rad(g)}" style="fill:none;stroke:${g === 0 ? "var(--now)" : "var(--grid)"};stroke-width:${g === 0 ? 1.4 : 1}"/>`;
    }
    s += `<text x="${cx + 4}" y="${cy - rad(lim) + 12}" style="font-size:10.5px">+${fmt(lim, 0)}%</text>`;
    s += `<text x="${cx + 4}" y="${cy - rad(0) + 12}" style="font-size:10.5px">0%</text>`;
    const names = [["right", 0], ["down", 90], ["left", 180], ["up", 270]];
    for (const [n, a] of names) {
      const t = a * Math.PI / 180;
      s += `<text x="${cx + Math.cos(t) * (R + 18)}" y="${cy + Math.sin(t) * (R + 18) + 4}" text-anchor="middle">${n}</text>`;
    }
    const pts = o.points.map((p) => { const t = p.angle * Math.PI / 180; const r = rad(Math.max(-lim, Math.min(lim, p.gain))); return [cx + Math.cos(t) * r, cy + Math.sin(t) * r, p]; });
    if (pts.length > 2) s += `<path d="${pts.map((p, i) => (i ? "L" : "M") + p[0] + "," + p[1]).join("")}Z" style="fill:var(--c2);fill-opacity:.12;stroke:var(--c2);stroke-width:2;stroke-linejoin:round"/>`;
    for (const [x, y, p] of pts) s += `<circle class="pp" cx="${x}" cy="${y}" r="4" data-a="${p.angle}" style="fill:var(--c2);stroke:var(--surface);stroke-width:2"/>`;
    s += `</svg>`;
    const bind = (el) => {
      el.querySelectorAll(".pp").forEach((c, i) => {
        c.addEventListener("mousemove", (ev) => {
          const p = o.points[i];
          showTip(`<b>Jumps towards ${fmt(p.angle, 0)}°</b><div class="r"><span>long/short</span><span>${p.gain >= 0 ? "+" : ""}${fmt(p.gain, 1)}%</span></div><div class="r"><span>sideways deviation</span><span>${fmt(p.side, 2)} mm</span></div><div class="r"><span>jumps</span><span>${p.n}</span></div>`, ev);
        });
        c.addEventListener("mouseleave", hideTip);
      });
    };
    return { svg: s, bind };
  }

  /* ---------------- dot plot with intervals ---------------- */
  function dots(w, o) {
    // rows: [{label, items:[{v, lo, hi, color, name}]}]
    const rowH = 30;
    const m = { l: o.ml || 130, r: 20, t: 8, b: 30 };
    const h = m.t + m.b + o.rows.length * rowH;
    let lo = Infinity, hi = -Infinity;
    for (const r of o.rows) for (const it of r.items) { lo = Math.min(lo, it.lo ?? it.v); hi = Math.max(hi, it.hi ?? it.v); }
    if (o.min != null) lo = o.min;
    if (!isFinite(lo)) { lo = 0; hi = 1; }
    const span = hi - lo || 1;
    lo -= span * 0.1; hi += span * 0.1;
    if (o.min != null) lo = o.min;
    const { ticks, step } = niceTicks(lo, hi, 4);
    const X = (v) => m.l + ((v - lo) / (hi - lo)) * (w - m.l - m.r);
    const f = o.fmt || ((v) => fmt(v, decimalsFor(step)));
    let s = `<svg viewBox="0 0 ${w} ${h}" width="${w}" height="${h}" role="img" aria-label="${esc(o.aria || "comparison")}">`;
    for (const t of ticks) { if (t < lo || t > hi) continue; s += `<line x1="${X(t)}" x2="${X(t)}" y1="${m.t}" y2="${h - m.b}" style="stroke:var(--grid)"/><text x="${X(t)}" y="${h - m.b + 16}" text-anchor="middle">${esc(f(t))}</text>`; }
    o.rows.forEach((r, i) => {
      const y = m.t + i * rowH + rowH / 2;
      s += `<text x="${m.l - 10}" y="${y + 4}" text-anchor="end" style="fill:var(--text-2)">${esc(r.label)}</text>`;
      r.items.forEach((it, j) => {
        const yy = y + (j - (r.items.length - 1) / 2) * 8;
        if (it.lo != null && it.hi != null) s += `<line x1="${X(it.lo)}" x2="${X(it.hi)}" y1="${yy}" y2="${yy}" style="stroke:${it.color};stroke-width:2;opacity:.55;stroke-linecap:round"/>`;
        s += `<circle class="dp" data-r="${i}" data-j="${j}" cx="${X(it.v)}" cy="${yy}" r="4.5" style="fill:${it.color};stroke:var(--surface);stroke-width:2"/>`;
      });
    });
    if (o.xLabel) s += `<text x="${(m.l + w - m.r) / 2}" y="${h - 2}" text-anchor="middle">${esc(o.xLabel)}</text>`;
    s += `</svg>`;
    const bind = (el) => {
      el.querySelectorAll(".dp").forEach((c) => {
        c.addEventListener("mousemove", (ev) => {
          const r = o.rows[+c.dataset.r], it = r.items[+c.dataset.j];
          showTip(`<b>${esc(r.label)} · ${esc(it.name)}</b><div class="r"><span>value</span><span>${esc(f(it.v))}</span></div>${it.lo != null ? `<div class="r"><span>range</span><span>${esc(f(it.lo))} – ${esc(f(it.hi))}</span></div>` : ""}${it.extra ? `<div class="r"><span>${esc(it.extra[0])}</span><span>${esc(it.extra[1])}</span></div>` : ""}`, ev);
        });
        c.addEventListener("mouseleave", hideTip);
      });
    };
    return { svg: s, bind };
  }

  /* ---------------- playfield of one play ---------------- */
  function playfield(w, o) {
    const pw = 512, ph = 384;
    const k = Math.min(w / (pw + 40), (o.height || 420) / (ph + 30));
    const W = w, H = Math.round(ph * k + 30);
    const ox = (W - pw * k) / 2, oy = 15;
    let s = `<svg viewBox="0 0 ${W} ${H}" width="${W}" height="${H}" role="img" aria-label="clicks on the playfield">`;
    s += `<rect x="${ox}" y="${oy}" width="${pw * k}" height="${ph * k}" rx="6" style="fill:var(--sunken);stroke:var(--line)"/>`;
    for (const p of o.hits) {
      const x = ox + p.tx * k, y = oy + p.ty * k;
      if (p.ex == null) { s += `<circle cx="${x}" cy="${y}" r="2.5" style="fill:none;stroke:var(--text-3)"/>`; continue; }
      const ex = x + p.ex * k * (o.mag || 3), ey = y + p.ey * k * (o.mag || 3);
      const miss = p.r === 3;
      s += `<line x1="${x}" y1="${y}" x2="${ex}" y2="${ey}" style="stroke:${miss ? "var(--bad)" : "var(--c1)"};stroke-width:${miss ? 1.6 : 1};opacity:${miss ? 0.95 : 0.55}"/>`;
      if (miss) s += `<circle cx="${ex}" cy="${ey}" r="2.6" style="fill:var(--bad)"/>`;
    }
    s += `</svg>`;
    return { svg: s };
  }

  window.Charts = { draw, line, target, tablet, polar, dots, playfield, fmt, corners, hideTip, showTip };
})();
