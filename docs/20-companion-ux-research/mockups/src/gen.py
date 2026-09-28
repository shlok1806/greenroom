import pathlib
D = pathlib.Path(__file__).parent

def page(body, title):
    return f"""<!doctype html><html><head><meta charset="utf-8"><title>{title}</title>
<link rel="stylesheet" href="base.css"></head><body><div class="win">{body}</div></body></html>"""

LIGHTS = '<div class="lights"><i></i><i></i><i></i></div>'

def sidebar(sel="tip", live=False):
    tip_icon = '<span class="ic spin"></span>' if live else '<span class="ic fail">&#x2715;</span>'
    tip_meta = 'checking' if live else '2 failed'
    needs = "" if live else f"""
      <h6>Needs you</h6>
      <div class="row sel">{tip_icon}<span class="t">TipSplit: split the bill</span><span class="m" style="color:var(--fail)">{tip_meta}</span></div>
      <div class="row"><span class="ic pass">&#x2713;</span><span class="t">WordCount: case buttons</span><span class="m">passed</span></div>"""
    running = f"""
      <h6>Running</h6>
      <div class="row sel">{tip_icon}<span class="t">TipSplit: split the bill</span><span class="m">4:18</span></div>""" if live else ""
    return f"""<div class="side">{needs}{running}
      <h6>Today</h6>
      <div class="row"><span class="ic pass">&#x2713;</span><span class="t">WordCount: longest word</span><span class="m">2:19</span></div>
      <div class="row"><span class="ic none"></span><span class="t">UnitConvert: result size</span><span class="m">0:36</span></div>
      <div class="row"><span class="ic fail">&#x2715;</span><span class="t">TodoList: Clear done</span><span class="m">0:22</span></div>
      <h6>Yesterday</h6>
      <div class="row"><span class="ic pass">&#x2713;</span><span class="t">TodoList: summary line</span><span class="m">21:57</span></div>
      <div class="row"><span class="ic pass">&#x2713;</span><span class="t">UnitConvert: Temperature</span><span class="m">20:10</span></div>
      <div class="row"><span class="ic none"></span><span class="t">WordCount: keeps text</span><span class="m">18:02</span></div>
    </div>"""

SHOT_MARK = '<div class="mark" style="left:31%;top:63%;width:25%;height:7%"></div>'

# Direction 1: timeline-first (finished, failed)
d1 = f"""
<div class="titlebar">{LIGHTS}<span class="tb-title">TipSplit: split the bill</span><span class="spacer"></span>
  <button class="btn">Reject</button><button class="btn danger">Accept fail</button></div>
<div class="body">{sidebar()}
  <div class="main">
    <div style="width:400px;border-right:1px solid var(--border);padding:20px 20px;overflow:hidden">
      <div class="h1" style="color:var(--fail)">Failed</div>
      <div class="muted" style="margin:2px 0 18px">Each pays is wrong at 25%.</div>
      <div style="display:flex;flex-direction:column;gap:2px">
        <div class="row"><span class="ic pass">&#x2713;</span><span class="t">Started the Mac</span><span class="m">0:21</span></div>
        <div class="row"><span class="ic pass">&#x2713;</span><span class="t">Opened TipSplit</span><span class="m">0:40</span></div>
        <div class="row"><span class="ic pass">&#x2713;</span><span class="t">Entered bill 120, 3 people</span><span class="m">1:12</span></div>
        <div class="row"><span class="ic pass">&#x2713;</span><span class="t">Tip is $24.00 at 20%</span><span class="m">1:30</span></div>
        <div class="row"><span class="ic fail">&#x2715;</span><span class="t">Each pays is $48.00</span><span class="m">1:31</span></div>
        <div class="row sel" style="flex-wrap:wrap"><span class="ic fail">&#x2715;</span><span class="t" style="font-weight:600">Each pays becomes $50.00 at 25%</span><span class="m">2:42</span>
          <div style="width:100%;padding-left:24px;margin-top:6px;color:var(--muted-fg)">Expected $50.00, saw $10.00</div></div>
        <div class="row"><span class="ic ring"></span><span class="t muted">Verdict: fail, waiting for you</span></div>
      </div>
      <div style="position:absolute;bottom:18px;left:260px"><button class="btn ghost">Show conversation</button></div>
    </div>
    <div style="flex:1;display:flex;flex-direction:column;min-width:0">
      <div class="shot" style="flex:1"><div style="position:relative"><img src="guest.png" style="width:600px">{SHOT_MARK}</div></div>
      <div style="padding:10px 16px;border-top:1px solid var(--border);display:flex;gap:10px;align-items:center">
        <span class="muted">&#9654;</span><div style="flex:1;height:4px;background:var(--muted);border-radius:2px;position:relative">
        <div style="position:absolute;left:0;width:78%;height:4px;background:var(--fg);border-radius:2px"></div>
        <div style="position:absolute;left:40%;top:-3px;width:3px;height:10px;background:var(--fail)"></div>
        <div style="position:absolute;left:78%;top:-3px;width:3px;height:10px;background:var(--fail)"></div></div>
        <span class="m muted">2:42 / 3:26</span></div>
    </div>
  </div>
</div>"""

# Direction 2: verdict-first checklist (finished, failed) - recommended
def d2(live=False):
    if live:
        head = f"""<div style="display:flex;align-items:center;gap:10px"><span class="pill live"><span class="dot"></span>Checking</span>
          <span class="h2">2 of 4 checks done</span></div>
          <div class="muted" style="margin-top:4px">Now: clicking 25%</div>"""
        actions = '<button class="btn">Take control</button>'
        checks = [
          ("pass", "Window shows Bill, Tip and People", ""),
          ("pass", "Tip is $24.00 for $120 at 20%", ""),
          ("spin", "Each pays is $48.00 for 3 people", "checking"),
          ("ring", "Each pays becomes $50.00 at 25%", ""),
        ]
        sel = 2
        right_caption = '<div class="caption">Clicked 25%</div>'
        mark = ""
    else:
        head = f"""<div style="display:flex;align-items:center;gap:10px"><span class="ic fail" style="width:22px;height:22px;font-size:12px">&#x2715;</span>
          <span class="h1">Failed</span><span class="muted" style="font-size:15px">2 of 4 checks</span></div>
          <div class="muted" style="margin-top:4px">The verifier proposes this. You decide.</div>"""
        actions = '<button class="btn">Reject</button><button class="btn primary">Accept fail</button>'
        checks = [
          ("pass", "Window shows Bill, Tip and People", ""),
          ("pass", "Tip is $24.00 for $120 at 20%", ""),
          ("fail", "Each pays is $48.00 for 3 people", "saw $8.00"),
          ("fail", "Each pays becomes $50.00 at 25%", "saw $10.00"),
        ]
        sel = 3
        right_caption = '<div class="caption">Expected <b>$50.00</b>, saw <b>$10.00</b></div>'
        mark = SHOT_MARK
    rows = ""
    for i, (k, t, note) in enumerate(checks):
        icon = {"pass": '<span class="ic pass">&#x2713;</span>', "fail": '<span class="ic fail">&#x2715;</span>',
                "spin": '<span class="ic spin"></span>', "ring": '<span class="ic ring"></span>'}[k]
        color = "var(--fail)" if k == "fail" else "var(--muted-fg)"
        rows += f"""<div class="row{' sel' if i == sel else ''}" style="padding:10px 10px">{icon}<span class="t" style="white-space:normal">{t}</span><span class="m" style="color:{color}">{note}</span></div>"""
    return f"""
<div class="titlebar">{LIGHTS}<span class="tb-title">TipSplit: split the bill</span><span class="spacer"></span><button class="btn ghost">&#8943;</button></div>
<div class="body">{sidebar(live=live)}
  <div class="main" style="flex-direction:column">
    <div style="display:flex;align-items:flex-start;padding:20px 24px 16px;border-bottom:1px solid var(--border)">
      <div style="flex:1">{head}</div><div style="display:flex;gap:8px">{actions}</div></div>
    <div style="flex:1;display:flex;min-height:0">
      <div style="width:360px;padding:12px 14px;border-right:1px solid var(--border);display:flex;flex-direction:column;gap:2px">
        <div class="muted" style="font-size:12px;font-weight:600;padding:4px 10px 6px">Checks</div>{rows}
        <div class="spacer"></div>
        <div style="display:flex;justify-content:space-between;align-items:center;padding:0 6px"><button class="btn ghost">Activity</button><button class="btn ghost">Message verifier</button></div>
      </div>
      <div style="flex:1;display:flex;flex-direction:column;min-width:0">
        <div class="shot" style="flex:1"><div style="position:relative"><img src="guest.png" style="width:640px">{mark}</div>{right_caption}</div>
        <div class="film">{''.join(f'<div class="f{" sel" if i == 9 else ""}{" bad" if (i in (6, 9) and not live) else ""}"></div>' for i in range(12))}</div>
      </div>
    </div>
  </div>
</div>"""

# Direction 3: screen-first with a thin rail (live)
d3 = f"""
<div class="titlebar">{LIGHTS}<span class="tb-title">TipSplit: split the bill</span><span class="pill live" style="margin-left:8px"><span class="dot"></span>Live</span><span class="spacer"></span>
  <button class="btn">Take control</button></div>
<div class="body">
  <div style="width:64px;flex:none;background:var(--side);border-right:1px solid var(--border);display:flex;flex-direction:column;align-items:center;gap:14px;padding-top:16px">
    <span class="ic spin"></span><span class="ic fail">&#x2715;</span><span class="ic pass">&#x2713;</span><span class="ic pass">&#x2713;</span><span class="ic none"></span><span class="ic fail">&#x2715;</span></div>
  <div class="main">
    <div style="flex:1;display:flex;flex-direction:column;min-width:0">
      <div class="shot" style="flex:1"><img src="guest.png" style="width:900px">
        <div class="caption"><span class="pill live" style="margin-right:6px"><span class="dot"></span>Checking</span>Clicked 25% and read Each pays</div></div>
      <div class="film">{''.join(f'<div class="f{" sel" if i == 13 else ""}{" bad" if i == 6 else ""}"></div>' for i in range(14))}</div>
    </div>
    <div style="width:300px;flex:none;border-left:1px solid var(--border);padding:20px 18px;display:flex;flex-direction:column;gap:14px">
      <div><div class="h2">Checking your change</div><div class="muted">2 of 4 checks done, 4:18</div></div>
      <div style="display:flex;flex-direction:column;gap:2px">
        <div class="row"><span class="ic pass">&#x2713;</span><span class="t">Window has the fields</span></div>
        <div class="row"><span class="ic pass">&#x2713;</span><span class="t">Tip at 20%</span></div>
        <div class="row sel"><span class="ic spin"></span><span class="t">Each pays for 3</span></div>
        <div class="row"><span class="ic ring"></span><span class="t muted">Each pays at 25%</span></div>
      </div>
      <div class="spacer"></div>
      <div style="border:1px solid var(--border);border-radius:8px;padding:8px 10px" class="muted">Message the verifier</div>
    </div>
  </div>
</div>"""

(D / "d1-timeline.html").write_text(page(d1, "Timeline first"))
(D / "d2-checklist-verdict.html").write_text(page(d2(False), "Checklist verdict"))
(D / "d2-checklist-live.html").write_text(page(d2(True), "Checklist live"))
(D / "d3-screen-rail.html").write_text(page(d3, "Screen first"))
print("ok")
