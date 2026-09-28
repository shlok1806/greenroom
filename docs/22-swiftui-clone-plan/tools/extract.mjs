// Ground-truth extractor for docs/22 (the SwiftUI clone plan).
//
// Renders one Beautiful UI piece at a time from a scratch `app/spec` page (see
// docs/22 section 4) in Chromium and WebKit at a fixed viewport and device scale,
// with the JS clock and every CSS animation under manual control, and writes:
//   specs/<piece>.json                 static computed styles, animations, motion curves
//   specs/<piece>/<engine>-<theme>.png settled state at 2x
//
// Usage: node extract.mjs <baseURL> <outDir> [piece ...]
// Needs: playwright 1.61 with its Chromium and WebKit builds installed.

import fs from "node:fs";
import path from "node:path";
import { chromium, webkit } from "playwright";

const [, , baseURL = "http://localhost:3917", outDir = "specs", ...only] = process.argv;

// piece id -> { variant, settleMs, sampleUntilMs }
const PIECES = {
  "task-rows": { v: "Capsules", settle: 9000, until: 7000 },
  "thinking-state": { v: "Steps", settle: 9000, until: 7800 },
  "tool-chips": { settle: 5000, until: 4200 },
  "loading-state": { v: "Drive", settle: 1300, until: 1300 },
  "chat-composer": { settle: 1500, until: 800 },
  shimmer: { settle: 1800, until: 1800 },
  "status-pill": { settle: 100, until: 0 },
  button: { settle: 100, until: 0 },
  "progress-ring": { settle: 800, until: 500 },
  "stream-text": { settle: 1500, until: 600 },
  "segmented-control": { settle: 400, until: 0 },
  chip: { settle: 100, until: 0 },
};

const FRAME = 1000 / 60; // 16.67 ms
const VIEWPORT = { width: 640, height: 480 };
const SCALE = 2;

// Runs in the page. Walks #spec-root and returns every element's box and the
// computed properties a SwiftUI port needs, with colors resolved to sRGB hex
// through a 1x1 canvas (so oklch() and color-mix() come back as numbers).
const snapshotFn = () => {
  const cv = document.createElement("canvas");
  cv.width = cv.height = 1;
  const cx = cv.getContext("2d", { willReadFrequently: true });
  const hex = (c) => {
    if (!c || c === "none") return c;
    if (c === "transparent" || /rgba?\(0, 0, 0, 0\)/.test(c)) return "transparent";
    cx.clearRect(0, 0, 1, 1);
    cx.fillStyle = "#000";
    cx.fillStyle = c;
    cx.fillRect(0, 0, 1, 1);
    const [r, g, b, a] = cx.getImageData(0, 0, 1, 1).data;
    const h =
      "#" +
      [r, g, b]
        .map((n) => n.toString(16).padStart(2, "0"))
        .join("")
        .toUpperCase();
    return a === 255 ? h : `${h}@${(a / 255).toFixed(3)}`;
  };
  // Resolve colors that appear inside compound values (box-shadow, gradients).
  const colorsIn = (s) =>
    s && s !== "none"
      ? s.replace(/(oklch|oklab|rgba?|color|hsla?)\([^()]*(\([^()]*\)[^()]*)*\)/g, (m) => hex(m))
      : s;
  const root = document.getElementById("spec-root");
  const origin = root.getBoundingClientRect();
  const out = [];
  const walk = (el, depth, pathStr) => {
    const cs = getComputedStyle(el);
    const r = el.getBoundingClientRect();
    const text = [...el.childNodes]
      .filter((n) => n.nodeType === 3)
      .map((n) => n.textContent.trim())
      .join(" ")
      .trim();
    out.push({
      path: pathStr,
      tag: el.tagName.toLowerCase(),
      cls: typeof el.className === "string" ? el.className : el.getAttribute("class"),
      text: text || undefined,
      frame: [r.x - origin.x, r.y - origin.y, r.width, r.height].map((n) => +n.toFixed(2)),
      style: {
        color: hex(cs.color),
        background: hex(cs.backgroundColor),
        backgroundImage: cs.backgroundImage !== "none" ? colorsIn(cs.backgroundImage) : undefined,
        fill: el instanceof SVGElement ? hex(cs.fill) : undefined,
        stroke: el instanceof SVGElement ? hex(cs.stroke) : undefined,
        strokeWidth: el instanceof SVGElement ? cs.strokeWidth : undefined,
        fontFamily: cs.fontFamily.split(",")[0],
        fontSize: cs.fontSize,
        fontWeight: cs.fontWeight,
        lineHeight: cs.lineHeight,
        letterSpacing: cs.letterSpacing,
        fontVariantNumeric: cs.fontVariantNumeric !== "normal" ? cs.fontVariantNumeric : undefined,
        fontFeatureSettings: cs.fontFeatureSettings,
        padding: [cs.paddingTop, cs.paddingRight, cs.paddingBottom, cs.paddingLeft].join(" "),
        gap: cs.gap !== "normal" ? cs.gap : undefined,
        radius: [
          cs.borderTopLeftRadius,
          cs.borderTopRightRadius,
          cs.borderBottomRightRadius,
          cs.borderBottomLeftRadius,
        ].join(" "),
        border:
          cs.borderTopWidth !== "0px" ||
          cs.borderBottomWidth !== "0px" ||
          cs.borderLeftWidth !== "0px"
            ? `${cs.borderTopWidth} ${cs.borderRightWidth} ${cs.borderBottomWidth} ${cs.borderLeftWidth} ${hex(cs.borderTopColor)} ${hex(cs.borderLeftColor)}`
            : undefined,
        shadow: cs.boxShadow !== "none" ? colorsIn(cs.boxShadow) : undefined,
        opacity: cs.opacity !== "1" ? cs.opacity : undefined,
        transform: cs.transform !== "none" ? cs.transform : undefined,
        filter: cs.filter !== "none" ? cs.filter : undefined,
      },
    });
    [...el.children].forEach((c, i) => {
      walk(c, depth + 1, `${pathStr}/${c.tagName.toLowerCase()}[${i}]`);
    });
  };
  walk(root.firstElementChild, 0, root.firstElementChild.tagName.toLowerCase());
  // Drop undefined keys to keep the JSON small and diffable.
  return JSON.parse(JSON.stringify(out));
};

async function run(engineName, engine, theme, font, piece, cfg, sampleCurves) {
  const browser = await engine.launch();
  const context = await browser.newContext({
    viewport: VIEWPORT,
    deviceScaleFactor: SCALE,
    reducedMotion: "no-preference",
  });
  await context.addInitScript((t) => {
    try {
      localStorage.setItem("bui-theme", t);
    } catch {}
  }, theme);
  // font "sf": swap Inter and JetBrains Mono for the faces the SwiftUI port ships
  // (SF Pro, SF Mono) and drop Inter-only features, so a port can be compared
  // glyph for glyph. font "inter": the page exactly as published.
  if (font === "sf") {
    await context.addInitScript(() => {
      const css =
        "body{--font-inter:-apple-system,'SF Pro Text',system-ui!important;" +
        "--font-mono-face:ui-monospace,'SF Mono',monospace!important;" +
        "font-feature-settings:normal!important}";
      const add = () => {
        const st = document.createElement("style");
        st.textContent = css;
        (document.head || document.documentElement).appendChild(st);
      };
      if (document.documentElement) add();
      else document.addEventListener("DOMContentLoaded", add);
    });
  }
  const page = await context.newPage();
  // Paused from the first instruction: every timer the page arms fires only when
  // runFor() advances virtual time, so t = 0 is the component mount.
  const T0 = new Date("2026-01-01T00:00:00Z");
  await page.clock.install({ time: T0 });
  await page.clock.pauseAt(T0);
  const url = `${baseURL}/spec?c=${piece}${cfg.v ? `&v=${cfg.v}` : ""}`;
  await page.goto(url, { waitUntil: "networkidle" });
  await page.waitForSelector("#spec-root > *");
  await page.evaluate(() => document.fonts.ready);

  // Every animation gets a virtual birth time the first step it is seen, then
  // is held paused at (now - birth). Timers advance only through the fake clock.
  await page.evaluate(() => {
    window.__births = new Map();
    window.__vt = 0;
    window.__seq = 0;
    window.__pin = () => {
      for (const a of document.getAnimations()) {
        if (!window.__births.has(a)) {
          window.__births.set(a, { t: window.__vt, id: ++window.__seq });
        }
        a.pause();
        a.currentTime = Math.max(0, window.__vt - window.__births.get(a).t);
      }
    };
    window.__pin();
  });

  const describe = () =>
    page.evaluate(() => {
      const desc = (el) => {
        if (!el) return null;
        const parts = [];
        let n = el;
        while (n && n.id !== "spec-root" && n.parentElement) {
          parts.unshift(`${n.tagName.toLowerCase()}[${[...n.parentElement.children].indexOf(n)}]`);
          n = n.parentElement;
        }
        return parts.join("/");
      };
      const seen = [];
      for (const [a, b] of window.__births) {
        const e = a.effect;
        const t = e.getTiming();
        seen.push({
          id: b.id,
          kind: a.constructor.name,
          name: a.animationName || a.transitionProperty || undefined,
          target: desc(e.target),
          bornAtMs: +b.t.toFixed(2),
          durationMs: t.duration,
          delayMs: t.delay,
          easing: t.easing,
          iterations: t.iterations === Infinity ? "infinite" : t.iterations,
          fill: t.fill,
          keyframes: e.getKeyframes().map((k) => {
            const o = { offset: +(+k.computedOffset).toFixed(4), easing: k.easing };
            for (const key of Object.keys(k)) {
              if (!["offset", "computedOffset", "easing", "composite"].includes(key))
                o[key] = k[key];
            }
            return o;
          }),
        });
      }
      return seen;
    });

  const curves = [];
  const last = new Map(); // target -> JSON of its last recorded row (delta encoding)
  // Step frame by frame all the way to the settle time, in every run: React
  // commits and arms its next timer between steps, so one big runFor() would
  // fire only the first timer of a scripted sequence.
  const steps = Math.round(cfg.settle / FRAME);
  for (let i = 0; i <= steps; i++) {
    // runFor() takes whole milliseconds, so step to the next frame boundary
    // (0, 17, 33, 50, ...) and read virtual time back from the page clock.
    if (i > 0) {
      const now = await page.evaluate((t0) => Date.now() - t0, T0.getTime());
      await page.clock.runFor(Math.max(1, Math.round(i * FRAME) - now));
    }
    const t = await page.evaluate((t0) => {
      window.__vt = Date.now() - t0;
      window.__pin();
      return window.__vt;
    }, T0.getTime());
    if (!sampleCurves || t > cfg.until) continue;
    // Sample every animated target: box, opacity, transform, background position.
    const sample = await page.evaluate(() => {
      const origin = document.getElementById("spec-root").getBoundingClientRect();
      const rows = [];
      const targets = new Set();
      for (const a of window.__births.keys()) if (a.effect?.target) targets.add(a.effect.target);
      for (const el of targets) {
        if (!el.isConnected) continue;
        const cs = getComputedStyle(el);
        const r = el.getBoundingClientRect();
        const p = [];
        let n = el;
        while (n && n.id !== "spec-root" && n.parentElement) {
          p.unshift(`${n.tagName.toLowerCase()}[${[...n.parentElement.children].indexOf(n)}]`);
          n = n.parentElement;
        }
        rows.push({
          target: p.join("/"),
          frame: [r.x - origin.x, r.y - origin.y, r.width, r.height].map((v) => +v.toFixed(2)),
          opacity: +(+cs.opacity).toFixed(4),
          transform: cs.transform,
          backgroundPosition: cs.backgroundImage !== "none" ? cs.backgroundPosition : undefined,
        });
      }
      return rows;
    });
    // Keep a row only when that target changed since its last recorded row.
    const changed = sample.filter((row) => {
      const key = JSON.stringify({ ...row, target: undefined });
      if (last.get(row.target) === key) return false;
      last.set(row.target, key);
      return true;
    });
    if (changed.length) curves.push({ tMs: t, sample: changed });
  }
  const animations = await describe();

  // Settle: finish every finite animation at the settle time.
  await page.evaluate(() => {
    window.__pin();
    for (const a of document.getAnimations()) {
      const it = a.effect.getTiming().iterations;
      if (it !== Infinity) a.finish();
    }
  });
  const settled = await page.evaluate(snapshotFn);
  const dir = path.join(outDir, piece);
  fs.mkdirSync(dir, { recursive: true });
  await page.locator("#spec-root").screenshot({
    path: path.join(dir, `${engineName}-${theme}-${font}.png`),
    animations: "disabled",
  });
  const version = browser.version();
  await browser.close();
  return { engine: engineName, version, theme, font, settled, animations, curves };
}

const pieces = only.length ? only : Object.keys(PIECES);
for (const piece of pieces) {
  const cfg = PIECES[piece];
  const result = {
    piece,
    source: "https://github.com/slev12397/beautiful-ui",
    sourceSha: "44a274e598395ab61e7c96c26fda2758780253b7",
    licence: "MIT, Copyright (c) 2026 Shane Levine",
    viewport: VIEWPORT,
    deviceScaleFactor: SCALE,
    frameMs: +FRAME.toFixed(4),
    settleMs: cfg.settle,
    variant: cfg.v,
    runs: [],
  };
  for (const [engineName, engine] of [
    ["chromium", chromium],
    ["webkit", webkit],
  ]) {
    for (const theme of ["light", "dark"]) {
      for (const font of ["inter", "sf"]) {
        // Motion curves and animation lists do not depend on engine, theme or
        // face; sample them once (chromium, light, sf) and keep settled styles
        // for all eight runs.
        const primary = engineName === "chromium" && theme === "light" && font === "sf";
        const r = await run(engineName, engine, theme, font, piece, cfg, primary);
        if (!primary) {
          delete r.curves;
          delete r.animations;
        }
        result.runs.push(r);
        console.log(`${piece} ${engineName} ${theme} ${font}: ${r.settled.length} nodes`);
      }
    }
  }
  fs.writeFileSync(path.join(outDir, `${piece}.json`), `${JSON.stringify(result, null, 1)}\n`);
}
