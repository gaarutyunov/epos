#!/usr/bin/env python3
"""
sysml2json.py — transform the constrained SysML v2 textual notation used by
model/epos.sysml into the SysML v2 API JSON that sysgo consumes.

This is a pragmatic, offline converter for environments where the OMG SysML v2
Pilot Implementation serializer (scripts/sysml2json.sh) cannot be downloaded.
It emits the simplified-but-valid API element form sysgo documents accepting
("Already have JSON from ... another export? Point source.file at it directly"):
a flat array of elements keyed by @id/@type/declaredName, containment expressed
via the derived `ownedElement` fast path, and typed features carrying inline
`type` / `direction` / `many` attributes.

sysgo's loader (internal/adapter/sysmlfile + internal/core/model) reads exactly
these fields, so the generated Go scaffold is identical to what the Pilot path
would produce for the same model semantics.

Usage:
    scripts/sysml2json.py model/epos.sysml model/model.json
"""
import json
import re
import sys


DEF_KINDS = {
    "attribute def": "AttributeDefinition",
    "part def": "PartDefinition",
    "port def": "PortDefinition",
    "item def": "ItemDefinition",
    "action def": "ActionDefinition",
    "requirement def": "RequirementDefinition",
}


def strip_comments(text):
    out = []
    for line in text.splitlines():
        # Remove // line comments (model uses no string literals with //).
        idx = line.find("//")
        if idx >= 0:
            line = line[:idx]
        out.append(line)
    return "\n".join(out)


class Parser:
    def __init__(self, text):
        self.elements = []
        self.text = text

    def add(self, eid, etype, name, owned=None, extra=None):
        el = {
            "@id": eid,
            "@type": etype,
            "declaredName": name,
            "isLibraryElement": False,
        }
        if owned:
            el["ownedElement"] = [{"@id": c} for c in owned]
        if extra:
            el.update(extra)
        self.elements.append(el)
        return eid

    def parse(self):
        # Top-level: package NAME { BODY }
        for name, body in self._blocks(self.text, r"package"):
            pkg_id = f"pkg-{name}"
            children = self._parse_package_body(name, body)
            self.add(pkg_id, "Package", name, owned=children)
        return self.elements

    def _parse_package_body(self, pkg, body):
        child_ids = []
        for kw, etype in DEF_KINDS.items():
            pattern = re.escape(kw)
            for dname, dbody in self._blocks(body, pattern):
                did = f"def-{pkg}-{dname}"
                members = self._parse_members(pkg, dname, dbody)
                self.add(did, etype, dname, owned=members)
                child_ids.append(did)
        return child_ids

    def _parse_members(self, pkg, defname, body):
        """Parse the '<...>;' member statements inside a def body."""
        member_ids = []
        # Remove any nested braces content already consumed as blocks; members
        # in epos.sysml are flat single-line 'kind name : Type[mult];' forms.
        for stmt in self._statements(body):
            m = self._parse_member(pkg, defname, stmt, len(member_ids))
            if m:
                member_ids.append(m)
        return member_ids

    def _parse_member(self, pkg, defname, stmt, idx):
        stmt = stmt.strip()
        if not stmt:
            return None
        direction = None
        tokens = stmt.split(None, 1)
        head = tokens[0]
        rest = tokens[1] if len(tokens) > 1 else ""

        if head in ("in", "out", "inout"):
            direction = "in" if head == "inout" else head
            # 'in item name : Type' or 'in name : Type'
            sub = rest.split(None, 1)
            if sub and sub[0] == "item":
                rest = sub[1] if len(sub) > 1 else ""
            etype = "ItemUsage"
        elif head == "attribute":
            etype = "AttributeUsage"
        elif head == "part":
            etype = "PartUsage"
        elif head == "port":
            etype = "PortUsage"
        elif head == "item":
            etype = "ItemUsage"
        elif head == "ref" or head == "reference":
            etype = "ReferenceUsage"
        else:
            return None

        # rest is 'name : Type[mult]'
        if ":" not in rest:
            return None
        lhs, rhs = rest.split(":", 1)
        name = lhs.strip()
        typ = rhs.strip().rstrip(";").strip()
        many = False
        mm = re.search(r"\[\s*\*\s*\]", typ)
        if mm:
            many = True
            typ = typ[: mm.start()].strip()
        # A '[0..1]' style optional (not used in epos model, but handle it).
        optional = False
        om = re.search(r"\[\s*0\s*\.\.\s*1\s*\]", rhs)
        if om:
            optional = True
        typ = re.sub(r"\[.*?\]", "", typ).strip()

        if not name or not typ:
            return None
        uid = f"usage-{pkg}-{defname}-{name}-{idx}"
        extra = {"type": typ}
        if direction:
            extra["direction"] = direction
        if many:
            extra["many"] = True
        if optional:
            extra["optional"] = True
        self.add(uid, etype, name, extra=extra)
        return uid

    @staticmethod
    def _statements(body):
        """Yield ';'-terminated statements, ignoring any brace groups."""
        depth = 0
        cur = []
        for ch in body:
            if ch == "{":
                depth += 1
                continue
            if ch == "}":
                depth = max(0, depth - 1)
                continue
            if depth > 0:
                continue
            if ch == ";":
                yield "".join(cur)
                cur = []
            else:
                cur.append(ch)
        tail = "".join(cur).strip()
        if tail:
            yield tail

    @staticmethod
    def _blocks(text, kw_pattern):
        """
        Find 'KW <name> { ... }' blocks at any position, returning (name, body)
        with balanced-brace bodies. KW may be a multi-word pattern like
        'attribute def'.
        """
        results = []
        # Match 'kw   Name   {'
        header = re.compile(kw_pattern + r"\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{")
        pos = 0
        while True:
            m = header.search(text, pos)
            if not m:
                break
            name = m.group(1)
            brace_start = m.end() - 1  # position of '{'
            depth = 0
            i = brace_start
            while i < len(text):
                if text[i] == "{":
                    depth += 1
                elif text[i] == "}":
                    depth -= 1
                    if depth == 0:
                        break
                i += 1
            body = text[brace_start + 1 : i]
            results.append((name, body))
            pos = i + 1
        return results


def main():
    if len(sys.argv) != 3:
        sys.stderr.write("usage: sysml2json.py <input.sysml> <output.json>\n")
        sys.exit(2)
    src = open(sys.argv[1], encoding="utf-8").read()
    src = strip_comments(src)
    parser = Parser(src)
    elements = parser.parse()
    # Wrap each element in the API envelope {"payload": ...} the pilot emits;
    # sysgo's loader unwraps it. A flat array would also work.
    doc = [{"payload": e, "identity": {"@id": e["@id"]}} for e in elements]
    with open(sys.argv[2], "w", encoding="utf-8") as f:
        json.dump(doc, f, indent=2)
        f.write("\n")
    sys.stderr.write(
        f"Wrote {sys.argv[2]} ({len(elements)} elements)\n"
    )


if __name__ == "__main__":
    main()
