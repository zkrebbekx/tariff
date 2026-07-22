/* tariff playground — vanilla JS front end over the WebAssembly engine.
 *
 * The engine exposes a global `tariff` object (see cmd/tariff-wasm). Every call
 * takes and returns plain JS objects; a returned {error, code} means the engine
 * rejected the call. There is no network and no framework. Rates cross in as
 * exact strings; amounts come back as int64 minor units plus a formatted
 * string, and the integer is always authoritative.
 */

(function () {
  "use strict";

  const NS = "http://www.w3.org/2000/svg";

  // Categorical palette for tier bands — legible on light and dark backgrounds.
  const TIER_COLORS = ["#4c6ef5", "#f2a63b", "#37b679", "#e06c9f", "#8a63d2"];

  let C = null; // the tariff engine (window.tariff)

  const state = {
    rounding: "half_up",
    grad: {
      // last tier carries no upTo. Rates are decimal strings shown to the user;
      // they cross to the engine verbatim and are parsed exactly.
      tiers: [
        { upTo: 5, rate: "7" },
        { upTo: 10, rate: "6.5" },
        { rate: "6" },
      ],
      qty: 6,
      maxQ: 15,
      series: [],
    },
    compose: {
      seq: 0,
      steps: [
        { id: 1, type: "charge", label: "Base charge", value: 100 },
        { id: 2, type: "percent_off", label: "10% off", value: 10 },
        { id: 3, type: "minimum", label: "Minimum $95", value: 95 },
      ],
    },
    pkMode: "package",
    stair: [
      { upTo: 10, flat: 10 },
      { upTo: 100, flat: 25 },
      { flat: 50 },
    ],
    proration: {
      startMs: Date.UTC(2024, 0, 1),
      endMs: Date.UTC(2024, 1, 1),
      atHours: 372, // 15.5 days = Jan 16 12:00
      basis: "second",
    },
  };

  // ---- tiny helpers --------------------------------------------------------

  function $(id) { return document.getElementById(id); }
  function el(tag, attrs) {
    const e = document.createElementNS(NS, tag);
    for (const k in attrs) e.setAttribute(k, attrs[k]);
    return e;
  }
  function clear(node) { while (node.firstChild) node.removeChild(node.firstChild); }
  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  }

  // ratToNum turns a canonical rational string ("13/2") or a decimal ("6.5")
  // into a JS number for display and charting only — never for money.
  function ratToNum(s) {
    if (s == null || s === "") return NaN;
    if (typeof s === "number") return s;
    const i = s.indexOf("/");
    if (i >= 0) return Number(s.slice(0, i)) / Number(s.slice(i + 1));
    return Number(s);
  }

  // money renders a formatted minor-unit string as a signed dollar amount, using
  // a real minus sign and a leading $.
  function money(formatted) {
    if (formatted == null) return "—";
    const neg = String(formatted).startsWith("-");
    const body = neg ? String(formatted).slice(1) : String(formatted);
    return (neg ? "−$" : "$") + body;
  }
  function moneyHTML(formatted) {
    if (formatted == null) return "—";
    const neg = String(formatted).startsWith("-");
    const body = neg ? String(formatted).slice(1) : String(formatted);
    return `<span class="cur">${neg ? "−$" : "$"}</span>${escapeHtml(body)}`;
  }

  const usd = () => ({ code: "USD", decimals: 2, rounding: state.rounding });
  const dollarsToMinor = (d) => Math.round(Number(d) * 100);

  // showErr surfaces an engine {error} in a panel's banner, or hides it.
  function showErr(id, resp) {
    const b = $(id);
    if (resp && resp.error) {
      b.textContent = resp.error;
      b.hidden = false;
      return true;
    }
    b.hidden = true;
    return false;
  }

  // =========================================================================
  //  PANEL 1 — GRADUATED VS VOLUME
  // =========================================================================

  function gradTiersDTO() {
    const t = state.grad.tiers;
    return [
      { upTo: t[0].upTo, unitRate: String(t[0].rate) },
      { upTo: t[1].upTo, unitRate: String(t[1].rate) },
      { last: true, unitRate: String(t[2].rate) },
    ];
  }

  function rateModel(model, qty) {
    return C.rate({ model, currency: usd(), tiers: gradTiersDTO(), quantity: qty });
  }

  // landedTierIndex returns which tier a quantity lands in, for colouring the
  // volume bar. Half-open bands: (prevUpTo, upTo].
  function landedTierIndex(qty) {
    const t = state.grad.tiers;
    if (qty <= t[0].upTo) return 0;
    if (qty <= t[1].upTo) return 1;
    return 2;
  }

  // buildTierEditor renders the schedule rows. It is rebuilt only on a preset or
  // scenario load so typing into a rate never loses focus.
  function buildTierEditor() {
    const host = $("tier-editor");
    clear(host);
    const t = state.grad.tiers;
    const bandText = [
      () => `1&ndash;`,
      () => `${t[0].upTo + 1}&ndash;`,
      () => `${t[1].upTo + 1}+`,
    ];
    for (let i = 0; i < 3; i++) {
      const row = document.createElement("div");
      row.className = "tier-row";
      const sw = document.createElement("span");
      sw.className = "tier-swatch";
      sw.style.background = TIER_COLORS[i];
      row.appendChild(sw);

      const band = document.createElement("span");
      band.className = "tier-band";
      if (i < 2) {
        band.innerHTML = bandText[i]();
        const up = document.createElement("input");
        up.type = "number"; up.min = "1"; up.value = String(t[i].upTo);
        up.setAttribute("aria-label", "tier upper bound");
        up.addEventListener("input", () => {
          const v = parseInt(up.value, 10);
          if (!isNaN(v) && v > 0) { t[i].upTo = v; onScheduleChange(); updateBandLabels(); }
        });
        band.appendChild(up);
      } else {
        band.innerHTML = bandText[i]();
      }
      row.appendChild(band);

      const rateWrap = document.createElement("span");
      rateWrap.className = "rate-input";
      rateWrap.innerHTML = `<span class="cur">$</span>`;
      const rate = document.createElement("input");
      rate.value = String(ratToNum(t[i].rate));
      rate.setAttribute("aria-label", "tier rate");
      rate.addEventListener("input", () => {
        if (rate.value.trim() !== "") { t[i].rate = rate.value.trim(); onScheduleChange(); }
      });
      rateWrap.appendChild(rate);
      row.appendChild(rateWrap);
      host.appendChild(row);
    }
  }

  // updateBandLabels refreshes the "6–" / "11+" text after an upTo edit without
  // rebuilding the inputs (which would drop focus mid-type).
  function updateBandLabels() {
    const rows = $("tier-editor").querySelectorAll(".tier-row .tier-band");
    const t = state.grad.tiers;
    if (rows[1]) rows[1].childNodes[0].textContent = `${t[0].upTo + 1}–`;
    if (rows[2]) rows[2].childNodes[0].textContent = `${t[1].upTo + 1}+`;
  }

  function onScheduleChange() {
    const t = state.grad.tiers;
    // Keep bounds sane: tier 1 must exceed tier 0.
    if (t[1].upTo <= t[0].upTo) t[1].upTo = t[0].upTo + 1;
    state.grad.maxQ = Math.min(40, Math.max(12, t[1].upTo + 5));
    const slider = $("grad-qty");
    slider.max = String(state.grad.maxQ);
    if (state.grad.qty > state.grad.maxQ) { state.grad.qty = state.grad.maxQ; slider.value = String(state.grad.qty); $("grad-qty-read").textContent = state.grad.qty; }
    computeSeries();
    renderGrad();
  }

  // computeSeries rates both models across the whole quantity range once, so the
  // line chart can be drawn and the quantity slider only moves a marker.
  function computeSeries() {
    const s = [];
    for (let q = 0; q <= state.grad.maxQ; q++) {
      const g = rateModel("graduated", q);
      const v = rateModel("volume", q);
      s.push({
        q,
        grad: g && !g.error ? g.total : null,
        vol: v && !v.error ? v.total : null,
      });
    }
    state.grad.series = s;
  }

  function renderGrad() {
    const q = state.grad.qty;
    const g = rateModel("graduated", q);
    const v = rateModel("volume", q);
    if (showErr("grad-err", g) || showErr("grad-err", v)) {
      $("grad-total").textContent = "—";
      $("vol-total").textContent = "—";
      return;
    }
    $("grad-total").innerHTML = moneyHTML(g.totalFormatted);
    $("vol-total").innerHTML = moneyHTML(v.totalFormatted);
    // Breakdowns
    $("grad-breakdown").textContent = g.lines.length
      ? g.lines.map((l) => `${l.quantity}×$${fmtRate(l.rate)}`).join(" + ")
      : "$0.00";
    $("vol-breakdown").textContent = v.lines.length
      ? `${v.lines[0].quantity} × $${fmtRate(v.lines[0].rate)}`
      : "$0.00";
    renderBars(g, v);
    renderLineChart();
  }

  function fmtRate(r) {
    const n = ratToNum(r);
    if (!isFinite(n)) return "0";
    return Number.isInteger(n) ? String(n) : n.toFixed(2).replace(/0$/, "");
  }

  // renderBars draws two horizontal cost bars: graduated (one segment per tier
  // touched) and volume (one segment at the landed rate). Bar length is scaled
  // by MONEY, so the longer bar is the more expensive interpretation.
  function renderBars(g, v) {
    const svg = $("bars-svg");
    clear(svg);
    const x0 = 18, barMaxW = 372, hgt = 46;
    const maxMoney = Math.max(g.total, v.total, 1);
    const scale = (amt) => (amt / maxMoney) * barMaxW;

    function drawBar(y, title, segments, totalFmt) {
      const t = el("text", { x: x0, y: y - 8, class: "bar-title", "font-size": "12", "font-weight": "600" });
      t.textContent = title;
      svg.appendChild(t);
      let x = x0;
      for (const s of segments) {
        const w = scale(s.amt);
        if (w > 0.5) {
          svg.appendChild(el("rect", { x, y, width: w, height: hgt, rx: 3, fill: s.color }));
          if (w > 42) {
            const lbl = el("text", { x: x + w / 2, y: y + hgt / 2 + 4, "text-anchor": "middle", "font-size": "11", class: "seg-label" });
            lbl.textContent = "$" + (s.amt / 100).toFixed(2);
            svg.appendChild(lbl);
          }
        }
        x += w;
      }
      const tot = el("text", { x: Math.max(x, x0) + 8, y: y + hgt / 2 + 5, "font-size": "15", class: "bar-total" });
      tot.textContent = money(totalFmt);
      svg.appendChild(tot);
    }

    const gradSegs = g.lines.map((l, i) => ({ amt: l.subtotal, color: TIER_COLORS[i % TIER_COLORS.length] }));
    if (gradSegs.length === 0) gradSegs.push({ amt: 0, color: TIER_COLORS[0] });
    const volColor = TIER_COLORS[landedTierIndex(state.grad.qty) % TIER_COLORS.length];
    const volSegs = [{ amt: v.total, color: volColor }];

    drawBar(34, "Graduated", gradSegs, g.totalFormatted);
    drawBar(132, "Volume", volSegs, v.totalFormatted);
  }

  function renderLineChart() {
    const svg = $("line-svg");
    clear(svg);
    const s = state.grad.series;
    const W = 560, Hh = 220;
    const m = { l: 46, r: 96, t: 22, b: 30 };
    const px0 = m.l, px1 = W - m.r, py0 = m.t, py1 = Hh - m.b;
    const maxQ = state.grad.maxQ;
    let maxY = 1;
    for (const p of s) { if (p.grad != null) maxY = Math.max(maxY, p.grad); if (p.vol != null) maxY = Math.max(maxY, p.vol); }
    maxY = Math.max(maxY, 100);
    const X = (q) => px0 + (q / maxQ) * (px1 - px0);
    const Y = (amt) => py1 - (amt / maxY) * (py1 - py0);

    // axes
    svg.appendChild(el("line", { x1: px0, y1: py1, x2: px1, y2: py1, class: "axis" }));
    svg.appendChild(el("line", { x1: px0, y1: py0, x2: px0, y2: py1, class: "axis" }));
    // y gridlines / ticks (4)
    for (let k = 0; k <= 4; k++) {
      const val = (maxY / 4) * k;
      const yy = Y(val);
      svg.appendChild(el("line", { x1: px0, y1: yy, x2: px1, y2: yy, class: "gridline" }));
      const tk = el("text", { x: px0 - 6, y: yy + 3, "text-anchor": "end", "font-size": "10" });
      tk.textContent = "$" + Math.round(val / 100);
      svg.appendChild(tk);
    }
    // x ticks
    const step = maxQ <= 15 ? 3 : 5;
    for (let q = 0; q <= maxQ; q += step) {
      const tk = el("text", { x: X(q), y: py1 + 16, "text-anchor": "middle", "font-size": "10" });
      tk.textContent = String(q);
      svg.appendChild(tk);
    }
    const xlab = el("text", { x: (px0 + px1) / 2, y: Hh - 2, "text-anchor": "middle", "font-size": "10", fill: "currentColor" });
    xlab.textContent = "quantity";
    svg.appendChild(xlab);

    function poly(key, cls) {
      const pts = s.filter((p) => p[key] != null).map((p) => `${X(p.q)},${Y(p[key])}`).join(" ");
      svg.appendChild(el("polyline", { points: pts, fill: "none", "stroke-width": "2.5", class: cls }));
    }
    poly("vol", "vol-line");
    poly("grad", "grad-line");

    // current-quantity marker
    const q = state.grad.qty;
    svg.appendChild(el("line", { x1: X(q), y1: py0, x2: X(q), y2: py1, class: "marker" }));
    const cur = state.grad.series[q];
    if (cur) {
      if (cur.grad != null) svg.appendChild(el("circle", { cx: X(q), cy: Y(cur.grad), r: 4, class: "grad-dot" }));
      if (cur.vol != null) svg.appendChild(el("circle", { cx: X(q), cy: Y(cur.vol), r: 4, class: "vol-dot" }));
    }

    // legend
    const lg = [
      { x: px1 + 8, y: py0 + 6, cls: "grad-line", t: "graduated" },
      { x: px1 + 8, y: py0 + 24, cls: "vol-line", t: "volume" },
    ];
    for (const item of lg) {
      svg.appendChild(el("line", { x1: item.x, y1: item.y, x2: item.x + 16, y2: item.y, "stroke-width": "3", class: item.cls }));
      const tx = el("text", { x: item.x + 22, y: item.y + 4, "font-size": "11" });
      tx.textContent = item.t;
      svg.appendChild(tx);
    }
  }

  // =========================================================================
  //  PANEL 2 — ORDER MATTERS (COMPOSITION)
  // =========================================================================

  const STEP_META = {
    charge: { type: "Charge", op: "+ $", labelDefault: "Charge" },
    percent_off: { type: "% off", op: "− ", labelDefault: "Discount", suffix: "%" },
    amount_off: { type: "Amount off", op: "− $", labelDefault: "Amount off" },
    minimum: { type: "Minimum", op: "≥ $", labelDefault: "Minimum" },
    credit: { type: "Draw credit", op: "− $", labelDefault: "Credit" },
  };

  function composeStepsDTO() {
    return state.compose.steps.map((s) => {
      switch (s.type) {
        case "charge":
          return { type: "charge", label: s.label, quantity: 1, charge: { model: "per_unit", currency: usd(), unitRate: String(s.value) } };
        case "percent_off":
          return { type: "percent_off", label: s.label, pct: String(Number(s.value) / 100) };
        case "amount_off":
          return { type: "amount_off", label: s.label, minor: dollarsToMinor(s.value) };
        case "minimum":
          return { type: "minimum", label: s.label, minor: dollarsToMinor(s.value) };
        case "credit":
          return { type: "credit", label: s.label, balance: dollarsToMinor(s.value) };
        default:
          return { type: s.type };
      }
    });
  }

  let dragId = null;

  function renderSteps() {
    const host = $("steps-list");
    clear(host);
    for (const s of state.compose.steps) {
      const meta = STEP_META[s.type];
      const card = document.createElement("div");
      card.className = "step-card";
      card.draggable = true;
      card.dataset.id = String(s.id);

      card.addEventListener("dragstart", () => { dragId = s.id; card.classList.add("dragging"); });
      card.addEventListener("dragend", () => { dragId = null; card.classList.remove("dragging"); host.querySelectorAll(".step-card").forEach((c) => c.classList.remove("drop-target")); });
      card.addEventListener("dragover", (e) => { e.preventDefault(); card.classList.add("drop-target"); });
      card.addEventListener("dragleave", () => card.classList.remove("drop-target"));
      card.addEventListener("drop", (e) => { e.preventDefault(); card.classList.remove("drop-target"); reorderStep(dragId, s.id); });

      const handle = document.createElement("span");
      handle.className = "step-handle"; handle.textContent = "☰"; handle.title = "Drag to reorder";
      card.appendChild(handle);

      const main = document.createElement("div");
      main.className = "step-main";
      const typ = document.createElement("span");
      typ.className = "step-type"; typ.textContent = meta.type;
      const body = document.createElement("div");
      body.className = "step-body";
      const op = document.createElement("span"); op.className = "op"; op.textContent = meta.op;
      const inp = document.createElement("input");
      inp.type = "number"; inp.min = "0"; inp.step = s.type === "percent_off" ? "1" : "0.01"; inp.value = String(s.value);
      inp.addEventListener("input", () => { const v = Number(inp.value); if (!isNaN(v)) { s.value = v; renderCompose(); } });
      body.appendChild(op); body.appendChild(inp);
      if (meta.suffix) { const sfx = document.createElement("span"); sfx.className = "op"; sfx.textContent = meta.suffix; body.appendChild(sfx); }
      main.appendChild(typ); main.appendChild(body);
      card.appendChild(main);

      const run = document.createElement("div");
      run.className = "step-running";
      run.innerHTML = `<span class="rlabel">running</span><span class="rval" data-run="${s.id}">—</span>`;
      card.appendChild(run);

      const del = document.createElement("button");
      del.className = "step-del"; del.type = "button"; del.textContent = "×"; del.title = "Remove";
      del.addEventListener("click", () => { state.compose.steps = state.compose.steps.filter((x) => x.id !== s.id); renderSteps(); renderCompose(); });
      card.appendChild(del);

      host.appendChild(card);
    }
  }

  function reorderStep(fromId, toId) {
    if (fromId == null || fromId === toId) return;
    const steps = state.compose.steps;
    const from = steps.findIndex((s) => s.id === fromId);
    const to = steps.findIndex((s) => s.id === toId);
    if (from < 0 || to < 0) return;
    const [moved] = steps.splice(from, 1);
    steps.splice(to, 0, moved);
    renderSteps();
    renderCompose();
  }

  function renderCompose() {
    const resp = C.compose({ currency: usd(), steps: composeStepsDTO() });
    const tbody = $("invoice-lines");
    clear(tbody);
    if (showErr("compose-err", resp)) {
      $("reconcile").textContent = "";
      return;
    }
    // per-step running totals on the cards, matched by order
    resp.steps.forEach((st, i) => {
      const s = state.compose.steps[i];
      if (!s) return;
      const cell = document.querySelector(`[data-run="${s.id}"]`);
      if (cell) {
        cell.textContent = money(st.runningFormatted);
        cell.classList.toggle("pos", st.effect > 0);
        cell.classList.toggle("neg", st.effect < 0);
      }
    });
    // invoice lines
    let lineSum = 0;
    for (const l of resp.lines) {
      lineSum += l.subtotal;
      const tr = document.createElement("tr");
      const label = l.label || "Charge";
      const rate = l.rate ? "$" + fmtRate(l.rate) : "";
      const amtCls = l.subtotal < 0 ? "num credit" : "num";
      tr.innerHTML =
        `<td>${escapeHtml(label)}</td>` +
        `<td class="num">${l.quantity || ""}</td>` +
        `<td class="num">${rate}</td>` +
        `<td class="${amtCls}">${money(l.subtotalFormatted)}</td>`;
      tbody.appendChild(tr);
    }
    const tr = document.createElement("tr");
    tr.className = "total-row";
    tr.innerHTML = `<td>Total</td><td></td><td></td><td class="num">${money(resp.totalFormatted)}</td>`;
    tbody.appendChild(tr);

    const rec = $("reconcile");
    if (lineSum === resp.total) {
      rec.className = "reconcile";
      rec.textContent = `Lines sum to ${money(resp.totalFormatted)} — reconciled ✓`;
    } else {
      rec.className = "reconcile bad";
      rec.textContent = `Lines sum to ${lineSum} minor ≠ total ${resp.total}`;
    }
  }

  // =========================================================================
  //  PANEL 3 — PACKAGE / STAIRSTEP
  // =========================================================================

  function buildStairEditor() {
    const host = $("pk-stair-editor");
    clear(host);
    const t = state.stair;
    const labels = [`1–`, `${t[0].upTo + 1}–`, `${t[1].upTo + 1}+`];
    for (let i = 0; i < 3; i++) {
      const row = document.createElement("div");
      row.className = "tier-row";
      row.appendChild(el2("span", "tier-swatch", "", { background: TIER_COLORS[i] }));
      const band = document.createElement("span");
      band.className = "tier-band";
      band.textContent = labels[i];
      if (i < 2) {
        const up = document.createElement("input");
        up.type = "number"; up.min = "1"; up.value = String(t[i].upTo);
        up.addEventListener("input", () => { const v = parseInt(up.value, 10); if (!isNaN(v) && v > 0) { t[i].upTo = v; renderPackage(); refreshStairLabels(); } });
        band.appendChild(up);
      }
      row.appendChild(band);
      const rw = document.createElement("span");
      rw.className = "rate-input"; rw.innerHTML = `<span class="cur">$</span>`;
      const flat = document.createElement("input");
      flat.type = "number"; flat.min = "0"; flat.step = "0.01"; flat.value = String(t[i].flat);
      flat.addEventListener("input", () => { const v = Number(flat.value); if (!isNaN(v)) { t[i].flat = v; renderPackage(); } });
      rw.appendChild(flat);
      row.appendChild(rw);
      host.appendChild(row);
    }
  }
  function refreshStairLabels() {
    const rows = $("pk-stair-editor").querySelectorAll(".tier-row .tier-band");
    const t = state.stair;
    if (rows[1]) rows[1].childNodes[0].textContent = `${t[0].upTo + 1}–`;
    if (rows[2]) rows[2].childNodes[0].textContent = `${t[1].upTo + 1}+`;
  }
  function el2(tag, cls, text, style) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text) e.textContent = text;
    if (style) for (const k in style) e.style[k] = style[k];
    return e;
  }

  function renderPackage() {
    const free = parseInt($("pk-free").value, 10) || 0;
    const qty = parseInt($("pk-qty").value, 10) || 0;
    let resp, note = "";
    if (state.pkMode === "package") {
      const N = parseInt($("pk-size").value, 10) || 1;
      const price = dollarsToMinor($("pk-price").value);
      resp = C.rate({ model: "package", currency: usd(), packageSize: N, packagePrice: price, freeAllowance: free, quantity: qty });
      const blocks = Math.max(0, Math.ceil((qty - free) / N));
      note = `blocks = ceil((${qty} − ${free}) / ${N}) = ${blocks}`;
    } else {
      const tiers = [
        { upTo: state.stair[0].upTo, flatRate: dollarsToMinor(state.stair[0].flat) },
        { upTo: state.stair[1].upTo, flatRate: dollarsToMinor(state.stair[1].flat) },
        { last: true, flatRate: dollarsToMinor(state.stair[2].flat) },
      ];
      resp = C.rate({ model: "stairstep", currency: usd(), tiers, freeAllowance: free, quantity: qty });
      const eff = Math.max(0, qty - free);
      note = `chargeable = ${qty} − ${free} = ${eff}; charges the one tier it lands in`;
    }
    const tbody = $("pk-lines");
    clear(tbody);
    if (showErr("pk-err", resp)) { $("pk-total").textContent = "—"; $("pk-note").textContent = ""; return; }
    $("pk-total").innerHTML = moneyHTML(resp.totalFormatted);
    $("pk-note").textContent = note;
    for (const l of resp.lines) {
      const tr = document.createElement("tr");
      const rate = l.rate ? "$" + fmtRate(l.rate) : "";
      tr.innerHTML =
        `<td>${escapeHtml(l.label || (state.pkMode === "package" ? "blocks" : "tier"))}</td>` +
        `<td class="num">${l.quantity || ""}</td>` +
        `<td class="num">${rate}</td>` +
        `<td class="num">${money(l.subtotalFormatted)}</td>`;
      tbody.appendChild(tr);
    }
  }

  function setPkMode(mode) {
    state.pkMode = mode;
    $("pk-mode-package").classList.toggle("active", mode === "package");
    $("pk-mode-stairstep").classList.toggle("active", mode === "stairstep");
    $("pk-package-fields").hidden = mode !== "package";
    $("pk-stairstep-fields").hidden = mode !== "stairstep";
    // Sensible defaults per model so the panel always shows a meaningful result.
    if (mode === "package") { $("pk-free").value = "100"; $("pk-qty").value = "201"; }
    else { $("pk-free").value = "0"; $("pk-qty").value = "55"; }
    renderPackage();
  }

  // =========================================================================
  //  PANEL 4 — PRORATION + CYCLE BOUNDARIES
  // =========================================================================

  function atISO() {
    return new Date(state.proration.startMs + state.proration.atHours * 3600 * 1000).toISOString();
  }
  function fmtAt(ms) {
    return new Date(ms).toLocaleString("en-US", { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", timeZone: "UTC", hour12: false });
  }

  function renderProration() {
    const p = state.proration;
    const resp = C.proration({
      oldAmount: dollarsToMinor($("pr-old").value),
      newAmount: dollarsToMinor($("pr-new").value),
      currency: usd(),
      period: { start: new Date(p.startMs).toISOString(), end: new Date(p.endMs).toISOString() },
      at: atISO(),
      basis: p.basis,
    });
    $("pr-at-read").textContent = fmtAt(p.startMs + p.atHours * 3600 * 1000);
    if (showErr("pr-err", resp)) {
      $("pr-credit").textContent = $("pr-charge").textContent = $("pr-net").textContent = "—";
      $("pr-frac").textContent = "";
      renderProrateBar(0);
      return;
    }
    $("pr-credit").innerHTML = moneyHTML(resp.creditFormatted);
    $("pr-charge").innerHTML = moneyHTML(resp.chargeFormatted);
    $("pr-net").innerHTML = moneyHTML(resp.netFormatted);
    const dec = resp.fractionDecimal;
    $("pr-frac").innerHTML =
      `Remaining fraction of the period: <code>${escapeHtml(resp.fraction || "0")}</code>` +
      (isFinite(dec) ? ` ≈ ${(dec * 100).toFixed(1)}%` : "") +
      ` &middot; basis <b>${p.basis === "day" ? "whole days" : "seconds"}</b>. ` +
      `Credit and charge are that fraction of each plan price, rounded once.`;
    renderProrateBar(isFinite(dec) ? dec : 0);
  }

  function renderProrateBar(remainingFrac) {
    const svg = $("prorate-svg");
    clear(svg);
    const x0 = 20, x1 = 540, y = 34, h = 30;
    const w = x1 - x0;
    const usedFrac = 1 - Math.max(0, Math.min(1, remainingFrac));
    const splitX = x0 + usedFrac * w;
    // used (left) and remaining (right)
    svg.appendChild(el("rect", { x: x0, y, width: Math.max(0, splitX - x0), height: h, rx: 3, class: "bar-used" }));
    svg.appendChild(el("rect", { x: splitX, y, width: Math.max(0, x1 - splitX), height: h, rx: 3, class: "bar-remain" }));
    svg.appendChild(el("rect", { x: x0, y, width: w, height: h, rx: 3, fill: "none", class: "bar-frame" }));
    // marker
    svg.appendChild(el("line", { x1: splitX, y1: y - 10, x2: splitX, y2: y + h + 10, class: "marker" }));
    // end labels
    const a = el("text", { x: x0, y: y - 12, "font-size": "11" }); a.textContent = "Jan 1";
    const b = el("text", { x: x1, y: y - 12, "text-anchor": "end", "font-size": "11" }); b.textContent = "Feb 1";
    svg.appendChild(a); svg.appendChild(b);
    // used / remaining captions
    const cu = el("text", { x: (x0 + splitX) / 2, y: y + h + 22, "text-anchor": "middle", "font-size": "10" });
    cu.textContent = "used (old plan)";
    const cr = el("text", { x: (splitX + x1) / 2, y: y + h + 22, "text-anchor": "middle", "font-size": "10" });
    cr.textContent = "remaining (new plan)";
    if (splitX - x0 > 60) svg.appendChild(cu);
    if (x1 - splitX > 70) svg.appendChild(cr);
  }

  function renderCycle() {
    const host = $("cycle-chips");
    clear(host);
    const anchorISO = "2025-01-31T00:00:00Z";
    const chips = [{ label: "Jan 31", anchor: true }];
    let fromISO = anchorISO;
    for (let k = 0; k < 4; k++) {
      const resp = C.boundary({ anchor: anchorISO, from: fromISO, unit: "monthly" });
      if (!resp || resp.error) break;
      const d = new Date(resp.next);
      chips.push({ label: d.toLocaleDateString("en-US", { month: "short", day: "numeric", timeZone: "UTC" }) });
      fromISO = resp.next;
    }
    chips.forEach((c, i) => {
      if (i > 0) { const arrow = el2("span", "cycle-arrow", "→"); host.appendChild(arrow); }
      host.appendChild(el2("span", "cycle-chip" + (c.anchor ? " anchor" : ""), c.label));
    });
  }

  // =========================================================================
  //  SCENARIOS
  // =========================================================================

  function buildScenarioButtons() {
    const host = $("scenario-buttons");
    clear(host);
    const list = C.scenarios || [];
    for (const sc of list) {
      const b = document.createElement("button");
      b.className = "btn"; b.type = "button"; b.textContent = sc.title;
      b.dataset.name = sc.name;
      b.addEventListener("click", () => loadScenario(sc.name, b));
      host.appendChild(b);
    }
  }

  function loadScenario(name, btn) {
    const sc = C.loadScenario(name);
    if (!sc || sc.error) return;
    document.querySelectorAll("#scenario-buttons .btn").forEach((b) => b.classList.toggle("active", b === btn));
    const note = $("scenario-note");
    note.innerHTML = `<b>${escapeHtml(sc.title)}.</b> ${escapeHtml(sc.note)}`;
    note.hidden = false;

    const cfg = sc.config || {};
    if (cfg.rounding) state.rounding = cfg.rounding;

    if (sc.panel === "graduated") {
      applyGradConfig(cfg);
      scrollTo("panel-graduated");
    } else if (sc.panel === "compose") {
      applyComposeConfig(cfg);
      scrollTo("panel-compose");
    } else if (sc.panel === "proration") {
      applyProrationConfig(cfg);
      scrollTo("panel-proration");
    }
  }

  function applyGradConfig(cfg) {
    if (Array.isArray(cfg.tiers) && cfg.tiers.length === 3) {
      state.grad.tiers = [
        { upTo: cfg.tiers[0].upTo, rate: String(ratToNum(cfg.tiers[0].unitRate)) },
        { upTo: cfg.tiers[1].upTo, rate: String(ratToNum(cfg.tiers[1].unitRate)) },
        { rate: String(ratToNum(cfg.tiers[2].unitRate)) },
      ];
    }
    if (typeof cfg.quantity === "number") state.grad.qty = cfg.quantity;
    buildTierEditor();
    $("grad-qty").value = String(state.grad.qty);
    $("grad-qty-read").textContent = state.grad.qty;
    onScheduleChange();
  }

  function applyComposeConfig(cfg) {
    if (Array.isArray(cfg.steps)) {
      let seq = 0;
      state.compose.steps = cfg.steps.map((s) => ({ id: ++seq, type: s.type, label: s.label, value: s.value }));
      state.compose.seq = seq;
    }
    renderSteps();
    renderCompose();
  }

  function applyProrationConfig(cfg) {
    if (typeof cfg.oldAmount === "number") $("pr-old").value = String(cfg.oldAmount);
    if (typeof cfg.newAmount === "number") $("pr-new").value = String(cfg.newAmount);
    if (cfg.basis) { state.proration.basis = cfg.basis; setBasisButtons(); }
    if (cfg.start) state.proration.startMs = Date.parse(cfg.start);
    if (cfg.end) state.proration.endMs = Date.parse(cfg.end);
    if (cfg.at) {
      const hrs = Math.round((Date.parse(cfg.at) - state.proration.startMs) / (3600 * 1000));
      state.proration.atHours = hrs;
      $("pr-at").value = String(hrs);
    }
    renderProration();
  }

  function setBasisButtons() {
    $("pr-basis-second").classList.toggle("active", state.proration.basis === "second");
    $("pr-basis-day").classList.toggle("active", state.proration.basis === "day");
  }

  function scrollTo(id) {
    const e = $(id);
    if (e) e.scrollIntoView({ behavior: "smooth", block: "start" });
  }

  // =========================================================================
  //  WIRING + BOOT
  // =========================================================================

  function wire() {
    // graduated
    $("grad-qty").addEventListener("input", (e) => {
      state.grad.qty = parseInt(e.target.value, 10);
      $("grad-qty-read").textContent = state.grad.qty;
      renderGrad();
    });
    $("grad-preset-stripe").addEventListener("click", () => {
      state.grad.tiers = [{ upTo: 5, rate: "7" }, { upTo: 10, rate: "6.5" }, { rate: "6" }];
      buildTierEditor(); onScheduleChange();
    });
    $("grad-preset-steep").addEventListener("click", () => {
      state.grad.tiers = [{ upTo: 5, rate: "10" }, { upTo: 10, rate: "10" }, { rate: "1" }];
      state.grad.qty = 11;
      buildTierEditor();
      $("grad-qty").value = "11"; $("grad-qty-read").textContent = 11;
      onScheduleChange();
    });

    // compose
    $("add-step-btn").addEventListener("click", () => {
      const type = $("add-step-type").value;
      const meta = STEP_META[type];
      const defVal = type === "percent_off" ? 10 : type === "minimum" ? 50 : type === "charge" ? 50 : 10;
      state.compose.steps.push({ id: ++state.compose.seq, type, label: meta.labelDefault, value: defVal });
      renderSteps(); renderCompose();
    });

    // package / stairstep
    $("pk-mode-package").addEventListener("click", () => setPkMode("package"));
    $("pk-mode-stairstep").addEventListener("click", () => setPkMode("stairstep"));
    ["pk-size", "pk-price", "pk-free", "pk-qty"].forEach((id) => $(id).addEventListener("input", renderPackage));

    // proration
    $("pr-at").addEventListener("input", (e) => { state.proration.atHours = parseInt(e.target.value, 10); renderProration(); });
    ["pr-old", "pr-new"].forEach((id) => $(id).addEventListener("input", renderProration));
    $("pr-basis-second").addEventListener("click", () => { state.proration.basis = "second"; setBasisButtons(); renderProration(); });
    $("pr-basis-day").addEventListener("click", () => { state.proration.basis = "day"; setBasisButtons(); renderProration(); });
  }

  let started = false;
  function start() {
    if (started) return;
    started = true;
    C = window.tariff;

    if (state.compose.steps.length) state.compose.seq = Math.max(...state.compose.steps.map((s) => s.id));

    buildTierEditor();
    buildStairEditor();
    buildScenarioButtons();
    wire();

    // initial render of every panel
    onScheduleChange();
    renderSteps();
    renderCompose();
    renderPackage();
    setBasisButtons();
    renderProration();
    renderCycle();

    $("boot").classList.add("hidden");

    // open on the flagship story
    const flagshipBtn = document.querySelector("#scenario-buttons .btn");
    loadScenario("graduated-vs-volume", flagshipBtn);
  }

  function boot() {
    if (!window.Go) { fail("wasm_exec.js failed to load."); return; }
    const go = new Go();
    const url = "tariff.wasm";
    window.__tariffOnReady = () => start();
    const inst = (obj) => { go.run(obj.instance); if (window.__tariffReady) start(); };
    if (WebAssembly.instantiateStreaming) {
      WebAssembly.instantiateStreaming(fetch(url), go.importObject).then(inst).catch((e) => fallback(go, url, inst, e));
    } else {
      fallback(go, url, inst);
    }
  }
  function fallback(go, url, inst) {
    fetch(url).then((r) => r.arrayBuffer()).then((buf) => WebAssembly.instantiate(buf, go.importObject)).then(inst)
      .catch((e) => fail("Could not load the wasm module: " + e));
  }
  function fail(msg) { const b = $("boot"); b.textContent = msg; b.classList.add("error"); }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", boot);
  } else {
    boot();
  }
})();
