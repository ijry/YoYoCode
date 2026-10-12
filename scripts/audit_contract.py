#!/usr/bin/env python3
"""Audit P0 route contracts: upstream params/response vs what we read.

Reads the upstream handler sources (read-only) and our Go handlers, then prints
two lists:

    == routes whose param names disagree ==
    route | missing=<upstream sends, we never read> | extra=<we read, upstream doesn't>

    == routes with no body-param read detected ==

`missing` is the one that breaks a client: it means the field the caller sends
is never looked at, so the value is silently dropped. `extra` is usually a
harmless alias we accept on top of upstream's name.

Known limits — a `missing` entry is only real if it survives all of these:

  * a helper that reads the body through a differently-named parameter
    (`func f(body map[string]any)` called as `f(d, c.Body)`) is not followed,
    so its keys are invisible;
  * a route registered as a shared func value rather than an inline closure
    (`reg.Register("x", someHandler)`) is not matched at all and shows up in
    the second list;
  * optional flags we deliberately ignore still count as `missing`.

So treat the output as a worklist to verify by hand, not as a verdict.
"""
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
UP = os.path.join(ROOT, "_upstream/ref/src-tauri/src/web/handlers")
INV = os.path.join(ROOT, "docs/ROUTE_INVENTORY.md")


def camel(name):
    parts = name.split("_")
    return parts[0] + "".join(p.title() for p in parts[1:])


def parse_structs(src):
    """Return {struct_name: [wire_field, ...]} honouring rename_all."""
    out = {}
    for m in re.finditer(
        r"((?:#\[[^\]]*\]\s*)*)pub struct (\w+)\s*\{(.*?)\n\}", src, re.S
    ):
        attrs, name, body = m.group(1), m.group(2), m.group(3)
        ren = re.search(r'rename_all\s*=\s*"(\w+)"', attrs)
        rename_all = ren.group(1) if ren else None
        fields = []
        for fm in re.finditer(r"pub (\w+)\s*:", body):
            f = fm.group(1)
            fields.append(camel(f) if rename_all == "camelCase" else f)
        out[name] = fields
    return out


def parse_handlers(src):
    """Return {fn_name: (param_type, response_type)}."""
    out = {}
    for m in re.finditer(
        r"pub async fn (\w+)\s*\((.*?)\)\s*->\s*Result<Json<(.*?)>,\s*\w+>",
        src,
        re.S,
    ):
        fn, args, resp = m.groups()
        pt = None
        # `Json(params): Json<XxxParams>` — take the TYPE, not the binding.
        pm = re.search(r"Json<(\w+)>", args) or re.search(r"Query<(\w+)>", args)
        if pm:
            pt = pm.group(1)
        elif "Json(" not in args and "Query(" not in args:
            pt = None  # a handler with no body params at all
        out[fn] = (pt, resp.strip())
    return out


def load_upstream():
    structs, handlers = {}, {}
    for name in os.listdir(UP):
        if not name.endswith(".rs"):
            continue
        src = open(os.path.join(UP, name), encoding="utf-8").read()
        structs.update(parse_structs(src))
        handlers.update(parse_handlers(src))
    return structs, handlers


def load_p0():
    routes = []
    for line in open(INV, encoding="utf-8"):
        m = re.match(r"- \[[ x]\] `/([a-z0-9_]+)`.*\*\*\[P0\]\*\*", line)
        if m:
            routes.append(m.group(1))
    return routes


def keys_in(body):
    """Every body key a handler body reads, via any of the Get* helpers."""
    keys = set()
    for m in re.finditer(
        r'Get(?:String|Int|IntOrErr|Bool|StringSlice|Float|Raw)\(\s*c,\s*((?:"[\w]+",?\s*)+)\)',
        body,
    ):
        keys |= set(re.findall(r'"(\w+)"', m.group(1)))
    for m in re.finditer(r'c\.Body\["(\w+)"\]', body):
        keys.add(m.group(1))
    return keys


def handler_bodies(src):
    """[(route, body)] for every `reg.Register("x", func(c *web.Context) ...)`."""
    out = []
    for m in re.finditer(
        r'reg(?:\.web)?\.Register\("(\w+)",\s*func\(c \*web\.Context\) \(any, error\) \{',
        src,
    ):
        brace = m.end() - 1
        depth, i = 0, brace
        while i < len(src):
            if src[i] == "{":
                depth += 1
            elif src[i] == "}":
                depth -= 1
                if depth == 0:
                    out.append((m.group(1), src[brace : i + 1]))
                    break
            i += 1
    return out


def func_bodies(blob):
    """{func_name: body} via brace matching.

    A regex on `\\n}` mis-slices Go: an earlier function whose body contains a
    line-final `}` swallows the next definition, which silently dropped
    helpers like pathParam(c) and made every git route look like it ignored
    `path`. Count braces instead.
    """
    out = {}
    for m in re.finditer(r"^func (?:\([^)]*\) )?(\w+)\(", blob, re.M):
        brace = blob.find("{", m.end())
        if brace < 0:
            continue
        depth, i, in_str, in_line = 0, brace, None, False
        while i < len(blob):
            ch = blob[i]
            if in_line:
                if ch == "\n":
                    in_line = False
            elif in_str:
                if ch == "\\":
                    i += 1
                elif ch == in_str:
                    in_str = None
            elif ch in "\"'`":
                in_str = ch
            elif ch == "/" and i + 1 < len(blob) and blob[i + 1] == "/":
                in_line = True
            elif ch == "{":
                depth += 1
            elif ch == "}":
                depth -= 1
                if depth == 0:
                    out[m.group(1)] = blob[brace : i + 1]
                    break
            i += 1
    return out


def load_ours():
    """{route: body keys}, resolving one level of helper indirection.

    Many handlers read their params through a helper (`pathParam(c)`), so a
    handler's own body alone under-reports what it accepts. Every package-level
    func is indexed by the keys it reads, and a handler inherits the keys of the
    helpers it calls.
    """
    base = os.path.join(ROOT, "src-go/web/handlers")
    srcs = []
    for name in os.listdir(base):
        if name.endswith(".go") and not name.endswith("_test.go"):
            srcs.append(open(os.path.join(base, name), encoding="utf-8").read())
    blob = "\n".join(srcs)

    func_bodies_map = func_bodies(blob)

    ours = {}
    for src in srcs:
        for route, body in handler_bodies(src):
            # Helpers delegate to helpers (`gitLog(c)` → `pathParam(c)`), so
            # follow the call graph to a fixed point instead of one hop.
            keys, seen, frontier = set(), set(), [body]
            while frontier:
                b = frontier.pop()
                keys |= keys_in(b)
                # A helper is followed when its first arg is the context
                # (`pathParam(c)`) or when it is handed the raw body
                # (`storeAgentConfigBlobs(d, agentType, c.Body)`).
                calls = set(re.findall(r"(\w+)\(c[,)]", b))
                calls |= set(re.findall(r"(\w+)\([^)]*c\.Body", b))
                for call in calls:
                    if call in func_bodies_map and call not in seen:
                        seen.add(call)
                        frontier.append(func_bodies_map[call])
            ours[route] = keys
    return ours


def main():
    structs, handlers = load_upstream()
    p0 = load_p0()
    ours = load_ours()
    print("== routes whose param names disagree (ours vs upstream wire) ==\n")
    bad = 0
    for r in p0:
        pt, resp = handlers.get(r, (None, "?"))
        if pt is None:
            continue
        want = {camel(f) for f in structs.get(pt, [])}
        if not want:
            continue
        got = {camel(k) for k in ours.get(r, set())}
        if not got:
            continue  # reads its params through a helper; checked by hand
        missing = sorted(want - got)
        extra = sorted(got - want)
        if missing or extra:
            bad += 1
            print("%-38s missing=%-38s extra=%s" % (r, ",".join(missing) or "-", ",".join(extra) or "-"))
    print("\n%d route(s) disagree." % bad)
    print("\n== routes with no body-param read detected (helper-based or absent) ==\n")
    for r in p0:
        pt, _ = handlers.get(r, (None, "?"))
        if pt is None:
            continue
        if not ours.get(r):
            print("  %s  (upstream params: %s)" % (r, ",".join(structs.get(pt, [])) or "none"))


main()
