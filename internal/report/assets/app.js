/* LANdlord report: charts, zoom, redaction toggle and CSV export. No network access. */
(function () {
  "use strict";

  function decodeBlock(id) {
    var el = document.getElementById(id);
    if (!el || typeof DecompressionStream === "undefined") {
      return Promise.reject(new Error("unsupported"));
    }
    var bin = atob(el.textContent.trim());
    var bytes = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    var stream = new Blob([bytes]).stream().pipeThrough(new DecompressionStream("gzip"));
    return new Response(stream).text();
  }

  var redactButton = document.getElementById("toggle-redact");
  if (redactButton) {
    redactButton.addEventListener("click", function () {
      var on = document.body.classList.toggle("redacted");
      redactButton.setAttribute("aria-pressed", String(on));
      redactButton.textContent = on ? "Show private details" : "Hide private details";
    });
  }

  var csvButton = document.getElementById("export-csv");
  if (csvButton) {
    csvButton.addEventListener("click", function () {
      decodeBlock("landlord-csv").then(function (text) {
        var url = URL.createObjectURL(new Blob([text], { type: "text/csv" }));
        var a = document.createElement("a");
        a.href = url;
        a.download = "landlord-data.csv";
        document.body.appendChild(a);
        a.click();
        a.remove();
        setTimeout(function () { URL.revokeObjectURL(url); }, 2000);
      });
    });
  }

  function showChartsError() {
    var note = document.getElementById("charts-error");
    var charts = document.getElementById("charts");
    if (note) note.hidden = false;
    if (charts) charts.hidden = true;
  }

  if (typeof uPlot === "undefined") {
    showChartsError();
    return;
  }

  decodeBlock("landlord-data").then(function (text) {
    start(JSON.parse(text));
  }).catch(showChartsError);

  function start(data) {
    var charts = [];
    var syncing = false;

    function cssVar(name) {
      return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
    }
    function slotColor(slot) {
      return cssVar(slot > 0 ? "--series-" + slot : "--series-other");
    }
    // Chart x values are already shifted into the session's local time; show them as-is.
    function tzDate(ts) {
      var d = new Date(ts * 1000);
      return new Date(d.getTime() + d.getTimezoneOffset() * 60000);
    }
    function fmt(v, unit) {
      if (v == null) return "-";
      var digits = Math.abs(v) < 10 ? 1 : 0;
      return v.toFixed(digits) + " " + unit;
    }

    var hop = data.hopLabel ? "Provider (first hop)" : null;
    var defs = [
      { id: "chart-latency", unit: "ms", series: [["gw_p95", "Router", "--series-1"], ["hop_p95", hop, "--series-2"], ["inet_p95", "Internet", "--series-3"]] },
      { id: "chart-loss", unit: "%", series: [["gw_loss", "Router", "--series-1"], ["hop_loss", hop, "--series-2"], ["inet_loss", "Internet", "--series-3"], ["udp_loss", "Call-like UDP", "--series-4"]] },
      { id: "chart-jitter", unit: "ms", series: [["inet_jitter", "Internet", "--series-3"], ["udp_jitter", "Call-like UDP", "--series-4"]] },
      { id: "chart-rssi", unit: "dBm", series: [["rssi", "Wi-Fi signal", "--series-1"]] },
      { id: "chart-rx", unit: "Mbit/s", series: [["rx", "Wi-Fi link speed", "--series-1"]] },
      { id: "chart-router", unit: "%", series: [["router_util", "Line in use", "--series-1"]] },
      { id: "chart-dns", unit: "ms", series: [["dns_ms", "Windows resolver", "--series-1"]] }
    ];

    function hasValues(arr) {
      if (!arr) return false;
      for (var i = 0; i < arr.length; i++) if (arr[i] != null) return true;
      return false;
    }

    function overlay(u) {
      var ctx = u.ctx;
      var box = u.bbox;
      var px = uPlot.pxRatio;
      ctx.save();
      ctx.beginPath();
      ctx.rect(box.left, box.top, box.width, box.height);
      ctx.clip();
      data.incidents.forEach(function (inc) {
        var x0 = u.valToPos(inc.s, "x", true);
        var x1 = u.valToPos(inc.e, "x", true);
        if (x1 < box.left || x0 > box.left + box.width) return;
        ctx.globalAlpha = 0.18;
        ctx.fillStyle = slotColor(inc.slot);
        ctx.fillRect(x0, box.top, Math.max(px, x1 - x0), box.height);
      });
      ctx.globalAlpha = 1;
      ctx.strokeStyle = cssVar("--text-muted");
      ctx.lineWidth = px;
      data.events.forEach(function (ev) {
        var x = Math.round(u.valToPos(ev.t, "x", true)) + 0.5;
        ctx.beginPath();
        ctx.moveTo(x, box.top);
        ctx.lineTo(x, box.top + 8 * px);
        ctx.stroke();
      });
      ctx.strokeStyle = cssVar("--text-primary");
      ctx.lineWidth = 2 * px;
      ctx.setLineDash([4 * px, 3 * px]);
      data.marks.forEach(function (m) {
        var x = u.valToPos(m.t, "x", true);
        ctx.beginPath();
        ctx.moveTo(x, box.top);
        ctx.lineTo(x, box.top + box.height);
        ctx.stroke();
      });
      ctx.restore();
    }

    function syncScale(u, key) {
      if (key !== "x" || syncing) return;
      syncing = true;
      var min = u.scales.x.min;
      var max = u.scales.x.max;
      charts.forEach(function (other) {
        if (other !== u) other.setScale("x", { min: min, max: max });
      });
      syncing = false;
    }

    function build() {
      var range = charts.length ? { min: charts[0].scales.x.min, max: charts[0].scales.x.max } : null;
      charts.forEach(function (c) { c.destroy(); });
      charts = [];
      defs.forEach(function (def) {
        var host = document.getElementById(def.id);
        if (!host) return;
        var present = def.series.filter(function (s) { return s[1] && hasValues(data.series[s[0]]); });
        var figure = host.closest("figure");
        if (!present.length) {
          if (figure) figure.hidden = true;
          return;
        }
        var muted = cssVar("--text-muted");
        var grid = cssVar("--grid");
        var opts = {
          width: Math.max(280, host.clientWidth),
          height: 170,
          tzDate: tzDate,
          cursor: { sync: { key: "landlord" }, drag: { x: true, y: false } },
          scales: { x: { time: true } },
          legend: { live: true },
          series: [{ value: function (u, v) { return v == null ? "-" : uPlot.fmtDate("{WWW} {D} {MMM} {HH}:{mm}:{ss}")(tzDate(v)); } }].concat(
            present.map(function (s) {
              return {
                label: s[1],
                stroke: cssVar(s[2]),
                width: 2,
                spanGaps: false,
                points: { show: false },
                value: function (u, v) { return fmt(v, def.unit); }
              };
            })
          ),
          axes: [
            { stroke: muted, grid: { stroke: grid, width: 1 }, ticks: { stroke: grid, width: 1 } },
            { stroke: muted, grid: { stroke: grid, width: 1 }, ticks: { show: false }, size: 64,
              values: function (u, vals) { return vals.map(function (v) { return v + " " + def.unit; }); } }
          ],
          hooks: { draw: [overlay], setScale: [syncScale] }
        };
        var values = [data.x].concat(present.map(function (s) { return data.series[s[0]]; }));
        host.textContent = "";
        charts.push(new uPlot(opts, values, host));
      });
      if (range) zoom(range.min, range.max);
    }

    function zoom(min, max) {
      if (!charts.length) return;
      charts[0].setScale("x", { min: min, max: max });
    }

    build();

    document.querySelectorAll("[data-zoom]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var parts = btn.getAttribute("data-zoom").split(",").map(Number);
        var pad = Math.max(300, parts[1] - parts[0]);
        zoom(parts[0] - pad, parts[1] + pad);
        var timeline = document.getElementById("timeline");
        if (timeline) timeline.scrollIntoView({ behavior: "smooth", block: "start" });
      });
    });

    var container = document.getElementById("charts");
    if (container && typeof ResizeObserver !== "undefined") {
      var lastWidth = container.clientWidth;
      new ResizeObserver(function () {
        if (Math.abs(container.clientWidth - lastWidth) < 4) return;
        lastWidth = container.clientWidth;
        charts.forEach(function (c) {
          c.setSize({ width: Math.max(280, c.root.parentElement.clientWidth), height: 170 });
        });
      }).observe(container);
    }

    if (window.matchMedia) {
      var mq = window.matchMedia("(prefers-color-scheme: dark)");
      if (mq.addEventListener) mq.addEventListener("change", build);
    }
  }
})();
