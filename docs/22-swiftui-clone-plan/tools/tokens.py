"""Beautiful UI tokens (app/globals.css @ 44a274e) from OKLCH to sRGB hex.

Standard OKLab -> linear sRGB (Ottosson) -> sRGB transfer, clipped per channel,
rounded to 8 bit. Alpha tokens print as hex@alpha.
"""
import json, math, re, sys

css = open(sys.argv[1]).read()


def block(sel):
    m = re.search(re.escape(sel) + r"\s*\{(.*?)\n\}", css, re.S)
    return m.group(1)


def to_hex(l, c, h, a=1.0):
    hr = math.radians(h)
    A, B = c * math.cos(hr), c * math.sin(hr)
    l_ = l + 0.3963377774 * A + 0.2158037573 * B
    m_ = l - 0.1055613458 * A - 0.0638541728 * B
    s_ = l - 0.0894841775 * A - 1.2914855480 * B
    L, M, S = l_ ** 3, m_ ** 3, s_ ** 3
    r = 4.0767416621 * L - 3.3077115913 * M + 0.2309699292 * S
    g = -1.2684380046 * L + 2.6097574011 * M - 0.3413193965 * S
    b = -0.0041960863 * L - 0.7034186147 * M + 1.7076147010 * S

    def enc(x):
        x = min(1, max(0, x))
        y = 12.92 * x if x <= 0.0031308 else 1.055 * x ** (1 / 2.4) - 0.055
        return round(y * 255)

    hx = "#%02X%02X%02X" % (enc(r), enc(g), enc(b))
    return hx if a == 1 else f"{hx}@{a:g}"


def parse(body):
    out = {}
    for name, val in re.findall(r"--([\w-]+):\s*oklch\(([^)]*)\);", body):
        parts = val.replace("/", " / ").split()
        l, c, h = map(float, parts[:3])
        a = float(parts[4]) if len(parts) > 4 else 1.0
        out[name] = to_hex(l, c, h, a)
    return out


light, dark = parse(block(":root")), parse(block(".dark"))
print(json.dumps({k: {"light": light[k], "dark": dark.get(k)} for k in light}, indent=1))
