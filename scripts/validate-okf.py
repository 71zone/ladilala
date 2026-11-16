#!/usr/bin/env python3
"""Strict Open Knowledge Format (OKF) v0.2 validator for the Ladilala vault bundle.

Derived from camen-workspace/scripts/validate-docs.py (VALIDATOR_VERSION 0.2.0):
the frontmatter parser, the ISO datetime and actor patterns and the link scanner
come from that file. The original file is not modified. Camen rules that do not
apply here (child repositories, camen metadata) are replaced by Ladilala rules.

Usage:
    python3 scripts/validate-okf.py [BUNDLE_DIR] [--ste]

BUNDLE_DIR defaults to the Ladilala folder of the NoteBook Obsidian vault.
--ste also prints sentence-length warnings (STE-100 style). Warnings never fail the run.
Exit code 0 means zero errors. Exit code 1 means at least one error, printed as
`file:line: message`.

Checks (errors):
  * every concept has parseable frontmatter and a non-empty `type`
  * `status` is draft | stable | deprecated; `ladilala` block holds scope=product
    and decision=open | proposed | decided
  * `generated`, `verified`, `stale_after` use ISO 8601 datetimes with a UTC offset
    and valid actors; `verified` never uses a `human:` actor (no human review is recorded)
  * `sources` entries have a `resource`; file:// resources exist
  * root index.md carries only okf_version "0.2"; other index.md and log.md have no frontmatter
  * every folder has an index.md that lists every concept and subfolder in it
  * log.md headings are `## YYYY-MM-DD`, newest first
  * links: no wikilinks, no absolute or file system links, no escape from the bundle, no broken link
  * no em-dash character; kebab-case file and folder names
"""

from __future__ import annotations

import argparse
import pathlib
import re
import sys
from typing import Any

VALIDATOR_VERSION = "0.1.0"
DEFAULT_BUNDLE = pathlib.Path(
    "/Users/8bu/Library/Mobile Documents/iCloud~md~obsidian/Documents/NoteBook/Ladilala"
)

VALID_STATUSES = {"draft", "stable", "deprecated"}
VALID_DECISIONS = {"open", "proposed", "decided"}
VALID_SCOPES = {"product"}
VALID_LADILALA_KEYS = {"scope", "decision"}

ISO_DATE_RE = re.compile(r"^\d{4}-\d{2}-\d{2}$")
ISO_DATETIME_RE = re.compile(
    r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$"
)
ACTOR_RE = re.compile(r"^(?:human:[^\s/]+|process:[^\s/]+|[^\s/:]+/[^\s/]+)$")
FENCE_RE = re.compile(r"^(`{3,}|~{3,})")
INLINE_CODE_RE = re.compile(r"`[^`]+`")
MD_LINK_RE = re.compile(r"(?<!!)\[[^\]]+\]\(([^)]+)\)")

def strip_inline_comment(line: str) -> str:
    in_single = False
    in_double = False
    for idx, ch in enumerate(line):
        if ch == "'" and not in_double:
            in_single = not in_single
        elif ch == '"' and not in_single:
            in_double = not in_double
        elif ch == "#" and not in_single and not in_double:
            if idx == 0 or line[idx - 1].isspace():
                return line[:idx].rstrip()
    return line.rstrip()


def parse_scalar(raw: str) -> Any:
    val = raw.strip()
    if not val:
        return ""
    if (val.startswith('"') and val.endswith('"')) or (
        val.startswith("'") and val.endswith("'")
    ):
        return val[1:-1]
    if val.startswith("[") and val.endswith("]"):
        inner = val[1:-1].strip()
        if not inner:
            return []
        return [parse_scalar(part) for part in split_top_level(inner, ",")]
    if val.startswith("{") and val.endswith("}"):
        inner = val[1:-1].strip()
        if not inner:
            return {}
        out: dict[str, Any] = {}
        for part in split_top_level(inner, ","):
            if ":" not in part:
                raise ValueError(f"invalid inline mapping entry: {part!r}")
            k, v = part.split(":", 1)
            out[k.strip()] = parse_scalar(v)
        return out
    if val == "true":
        return True
    if val == "false":
        return False
    if val.isdigit():
        return int(val)
    return val


def split_top_level(text: str, sep: str) -> list[str]:
    parts: list[str] = []
    cur: list[str] = []
    depth = 0
    in_single = False
    in_double = False
    for ch in text:
        if ch == "'" and not in_double:
            in_single = not in_single
        elif ch == '"' and not in_single:
            in_double = not in_double
        elif not in_single and not in_double:
            if ch in "[{":
                depth += 1
            elif ch in "]}":
                depth -= 1
            elif ch == sep and depth == 0:
                parts.append("".join(cur).strip())
                cur = []
                continue
        cur.append(ch)
    if cur:
        parts.append("".join(cur).strip())
    return parts


def parse_yaml_frontmatter(lines: list[str]) -> dict[str, Any]:
    root: dict[str, Any] = {}
    idx = 0
    n = len(lines)

    while idx < n:
        raw_line = strip_inline_comment(lines[idx])
        if not raw_line.strip():
            idx += 1
            continue
        indent = len(raw_line) - len(raw_line.lstrip(" "))
        if indent != 0:
            raise ValueError(f"unexpected indentation on line {idx + 1}: {lines[idx]!r}")
        stripped = raw_line.strip()
        if ":" not in stripped:
            raise ValueError(f"expected key: value on line {idx + 1}: {lines[idx]!r}")
        key, rest = stripped.split(":", 1)
        key = key.strip()
        rest = rest.strip()
        if rest:
            root[key] = parse_scalar(rest)
            idx += 1
            continue

        idx += 1
        block_lines: list[tuple[int, str]] = []
        while idx < n:
            sub_raw = strip_inline_comment(lines[idx])
            if not sub_raw.strip():
                idx += 1
                continue
            sub_indent = len(sub_raw) - len(sub_raw.lstrip(" "))
            if sub_indent == 0:
                break
            block_lines.append((sub_indent, sub_raw.strip()))
            idx += 1

        if not block_lines:
            root[key] = None
            continue

        if block_lines[0][1].startswith("- "):
            items: list[Any] = []
            cur_item: dict[str, Any] | None = None
            for _, bline in block_lines:
                if bline.startswith("- "):
                    after_dash = bline[2:].strip()
                    if after_dash.startswith("{") and after_dash.endswith("}"):
                        if cur_item is not None:
                            items.append(cur_item)
                            cur_item = None
                        items.append(parse_scalar(after_dash))
                    elif ":" in after_dash:
                        if cur_item is not None:
                            items.append(cur_item)
                        k, v = after_dash.split(":", 1)
                        cur_item = {k.strip(): parse_scalar(v)}
                    else:
                        if cur_item is not None:
                            items.append(cur_item)
                            cur_item = None
                        items.append(parse_scalar(after_dash))
                else:
                    if cur_item is None or ":" not in bline:
                        raise ValueError(f"invalid list item continuation: {bline!r}")
                    k, v = bline.split(":", 1)
                    cur_item[k.strip()] = parse_scalar(v)
            if cur_item is not None:
                items.append(cur_item)
            root[key] = items
        else:
            sub_map: dict[str, Any] = {}
            for _, bline in block_lines:
                if ":" not in bline:
                    raise ValueError(f"invalid mapping entry in {key!r}: {bline!r}")
                k, v = bline.split(":", 1)
                sub_map[k.strip()] = parse_scalar(v)
            root[key] = sub_map

    return root


def split_frontmatter(text: str) -> tuple[dict[str, Any] | None, str, str | None]:
    lines = text.splitlines()
    if not lines or lines[0].strip() != "---":
        return None, text, None
    end_idx = None
    for idx in range(1, len(lines)):
        if lines[idx].strip() == "---":
            end_idx = idx
            break
    if end_idx is None:
        return None, text, "unclosed YAML frontmatter block (missing closing '---')"
    try:
        meta = parse_yaml_frontmatter(lines[1:end_idx])
    except ValueError as exc:
        return None, text, f"invalid YAML frontmatter: {exc}"
    body = "\n".join(lines[end_idx + 1 :])
    return meta, body, None


KEBAB_FILE_RE = re.compile(r"^[a-z0-9]+(?:-[a-z0-9]+)*\.md$")
KEBAB_DIR_RE = re.compile(r"^[a-z0-9]+(?:-[a-z0-9]+)*$")
WIKILINK_RE = re.compile(r"\[\[[^\]]+\]\]")
LIST_ENTRY_RE = re.compile(r"^\* \[([^\]]+)\]\(([^)]+)\) - \S")
SENTENCE_SPLIT_RE = re.compile(r"(?<=[.!?])\s+")
EM_DASH = "\u2014"


class Report:
    def __init__(self) -> None:
        self.errors: list[str] = []
        self.warnings: list[str] = []

    def err(self, rel: str, line: int | None, msg: str) -> None:
        self.errors.append(f"{rel}:{line}: {msg}" if line else f"{rel}: {msg}")

    def warn(self, rel: str, line: int, msg: str) -> None:
        self.warnings.append(f"{rel}:{line}: {msg}")


def check_frontmatter_fields(meta: dict[str, Any], md: pathlib.Path, rel: str, r: Report) -> None:
    t = meta.get("type")
    if not isinstance(t, str) or not t.strip():
        r.err(rel, 1, "concept requires a non-empty string 'type'")
    for key in ("title", "description", "resource"):
        if key in meta and (not isinstance(meta[key], str) or not meta[key].strip()):
            r.err(rel, 1, f"'{key}' must be a non-empty string when present")
    for key in ("title", "description", "tags", "status"):
        if key not in meta:
            r.err(rel, 1, f"house style requires '{key}'")
    tags = meta.get("tags")
    if "tags" in meta:
        if not isinstance(tags, list) or not tags or not all(isinstance(x, str) and x.strip() for x in tags):
            r.err(rel, 1, "'tags' must be a non-empty list of strings")
        elif any(x != x.lower() for x in tags):
            r.err(rel, 1, "'tags' must be lowercase")
    if "status" in meta and meta["status"] not in VALID_STATUSES:
        r.err(rel, 1, f"invalid status {meta['status']!r} (expected {sorted(VALID_STATUSES)})")
    if "stale_after" in meta:
        v = meta["stale_after"]
        if not isinstance(v, str) or not ISO_DATETIME_RE.match(v):
            r.err(rel, 1, f"'stale_after' must be an ISO 8601 datetime with UTC offset, got {v!r}")
    if "generated" in meta:
        g = meta["generated"]
        if not isinstance(g, dict) or not isinstance(g.get("by"), str):
            r.err(rel, 1, "'generated' must be a mapping with 'by'")
        else:
            if not ACTOR_RE.match(g["by"]):
                r.err(rel, 1, f"'generated.by' is not a valid actor: {g['by']!r}")
            if "at" in g and (not isinstance(g["at"], str) or not ISO_DATETIME_RE.match(g["at"])):
                r.err(rel, 1, f"'generated.at' must be an ISO 8601 datetime with UTC offset, got {g['at']!r}")
    if "verified" in meta:
        v = meta["verified"]
        items = [v] if isinstance(v, dict) else v
        if not isinstance(items, list) or not items:
            r.err(rel, 1, "'verified' must be a mapping or a non-empty list of mappings")
        else:
            for e in items:
                if not isinstance(e, dict):
                    r.err(rel, 1, "each 'verified' entry must be a mapping")
                    continue
                by, at = e.get("by"), e.get("at")
                if not isinstance(by, str) or not ACTOR_RE.match(by):
                    r.err(rel, 1, f"'verified.by' is not a valid actor: {by!r}")
                elif by.startswith("human:"):
                    r.err(rel, 1, "'verified' with a human: actor is not allowed (no human review is recorded)")
                if not isinstance(at, str) or not ISO_DATETIME_RE.match(at):
                    r.err(rel, 1, f"'verified.at' must be an ISO 8601 datetime with UTC offset, got {at!r}")
    if "sources" in meta:
        srcs = meta["sources"]
        if not isinstance(srcs, list) or not srcs:
            r.err(rel, 1, "'sources' must be a non-empty list when present")
        else:
            for s in srcs:
                if not isinstance(s, dict):
                    r.err(rel, 1, "each 'sources' entry must be a mapping")
                    continue
                res = s.get("resource")
                if not isinstance(res, str) or not res.strip():
                    r.err(rel, 1, "each 'sources' entry requires a non-empty 'resource'")
                elif res.startswith("file://") and not pathlib.Path(res[7:]).exists():
                    r.err(rel, 1, f"sources.resource {res!r} does not exist")
                elif res.startswith(("./", "../", "/")):
                    r.err(rel, 1, f"sources.resource {res!r} must be a URL or file:// URI (bundle is self-contained)")
                if "last_modified" in s and not (isinstance(s["last_modified"], str) and ISO_DATETIME_RE.match(s["last_modified"])):
                    r.err(rel, 1, "'sources.last_modified' must be an ISO 8601 datetime with UTC offset")
    if "ladilala" in meta:
        w = meta["ladilala"]
        if not isinstance(w, dict):
            r.err(rel, 1, "'ladilala' must be a mapping")
        else:
            unknown = set(w) - VALID_LADILALA_KEYS
            if unknown:
                r.err(rel, 1, f"unknown keys in 'ladilala': {sorted(unknown)}")
            if w.get("scope") not in VALID_SCOPES:
                r.err(rel, 1, f"'ladilala.scope' must be one of {sorted(VALID_SCOPES)}, got {w.get('scope')!r}")
            if "decision" in w and w["decision"] not in VALID_DECISIONS:
                r.err(rel, 1, f"'ladilala.decision' must be one of {sorted(VALID_DECISIONS)}, got {w['decision']!r}")
    if md.parent.name == "decisions" and md.name != "index.md":
        if meta.get("type") != "Architecture Decision":
            r.err(rel, 1, "files under decisions/ must have type 'Architecture Decision'")
        if not isinstance(meta.get("ladilala"), dict) or "decision" not in meta.get("ladilala", {}):
            r.err(rel, 1, "ADR requires ladilala.decision")


def check_text(md: pathlib.Path, bundle: pathlib.Path, text: str, rel: str, r: Report, ste: bool) -> None:
    in_fence = False
    resolved_root = bundle.resolve()
    for lineno, line in enumerate(text.splitlines(), 1):
        if EM_DASH in line:
            r.err(rel, lineno, "em-dash character is not allowed")
        if FENCE_RE.match(line.strip()):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        clean = INLINE_CODE_RE.sub("", line)
        if WIKILINK_RE.search(clean):
            r.err(rel, lineno, "wikilinks are not allowed; use a relative markdown link")
        for m in MD_LINK_RE.finditer(clean):
            raw = m.group(1).strip()
            if not raw or raw.startswith("#") or raw.startswith(("mailto:", "tel:")):
                continue
            if raw.startswith(("http://", "https://")):
                continue
            if raw.startswith(("file:", "~")) or raw.startswith("/"):
                r.err(rel, lineno, f"absolute or file system link {raw!r} is not allowed")
                continue
            target = raw.split("#", 1)[0].strip()
            if not target:
                continue
            resolved = (md.parent / target).resolve()
            try:
                resolved.relative_to(resolved_root)
            except ValueError:
                r.err(rel, lineno, f"link {raw!r} escapes the bundle")
                continue
            if not resolved.exists():
                r.err(rel, lineno, f"broken relative link {raw!r}")
        if ste and not line.lstrip().startswith(("|", "#")):
            plain = MD_LINK_RE.sub(lambda mm: mm.group(0).split("]")[0][1:], clean)
            body = re.sub(r"^\s*(?:[*\-]|\d+\.)\s+", "", plain)
            for sent in SENTENCE_SPLIT_RE.split(body):
                words = len(sent.split())
                if words > 25:
                    r.warn(rel, lineno, f"sentence has {words} words (limit 25): {sent[:60]}...")


def check_index(idx: pathlib.Path, bundle: pathlib.Path, r: Report) -> None:
    rel = idx.relative_to(bundle).as_posix()
    folder = idx.parent
    listed: set[str] = set()
    for lineno, line in enumerate(idx.read_text(encoding="utf-8").splitlines(), 1):
        if line.startswith("* ["):
            m = LIST_ENTRY_RE.match(line)
            if not m:
                r.err(rel, lineno, "list entry must read '* [Title](path) - one-sentence description'")
                continue
            target = m.group(2).split("#", 1)[0]
            if not target.startswith(("http://", "https://")):
                listed.add((folder / target).resolve().as_posix().rstrip("/"))
    for child in sorted(folder.iterdir()):
        if child.name.startswith(".") or child.name in ("index.md", "log.md"):
            continue
        if child.is_file() and child.suffix == ".md" or child.is_dir():
            key = child.resolve().as_posix().rstrip("/")
            if key not in listed and (key + "/index.md") not in listed:
                r.err(rel, None, f"{child.name}{'/' if child.is_dir() else ''} is not listed in this index.md")


def check_log(lg: pathlib.Path, bundle: pathlib.Path, r: Report) -> None:
    rel = lg.relative_to(bundle).as_posix()
    dates: list[str] = []
    for lineno, line in enumerate(lg.read_text(encoding="utf-8").splitlines(), 1):
        if line.startswith("## "):
            h = line[3:].strip()
            if not ISO_DATE_RE.match(h):
                r.err(rel, lineno, f"log heading must be '## YYYY-MM-DD', got {h!r}")
            else:
                dates.append(h)
    if dates != sorted(dates, reverse=True):
        r.err(rel, None, "log entries must be newest first")


def validate(bundle: pathlib.Path, ste: bool) -> Report:
    r = Report()
    if not bundle.is_dir():
        r.err(str(bundle), None, "bundle directory does not exist")
        return r
    for d in sorted(p for p in bundle.rglob("*") if p.is_dir()):
        rel_dir = d.relative_to(bundle)
        if any(part.startswith(".") for part in rel_dir.parts):
            continue
        if not KEBAB_DIR_RE.match(d.name):
            r.err(rel_dir.as_posix(), None, "folder name must be lowercase kebab-case")
        if not (d / "index.md").is_file():
            r.err(rel_dir.as_posix(), None, "folder has no index.md")
    if not (bundle / "index.md").is_file():
        r.err("index.md", None, "bundle root has no index.md")
    for md in sorted(bundle.rglob("*.md")):
        rel_path = md.relative_to(bundle)
        if any(part.startswith(".") for part in rel_path.parts):
            continue
        rel = rel_path.as_posix()
        text = md.read_text(encoding="utf-8")
        meta, body, fm_err = split_frontmatter(text)
        if fm_err:
            r.err(rel, 1, fm_err)
            continue
        if md.name != "index.md" and md.name != "log.md" and not KEBAB_FILE_RE.match(md.name):
            r.err(rel, None, "file name must be lowercase kebab-case")
        if md.name == "index.md":
            if md.parent == bundle:
                if meta is None or set(meta) != {"okf_version"} or meta.get("okf_version") != "0.2":
                    r.err(rel, 1, "root index.md must carry only okf_version: \"0.2\" in frontmatter")
            elif meta is not None:
                r.err(rel, 1, "non-root index.md must not have frontmatter")
            check_index(md, bundle, r)
        elif md.name == "log.md":
            if meta is not None:
                r.err(rel, 1, "log.md must not have frontmatter")
            check_log(md, bundle, r)
        else:
            if meta is None:
                r.err(rel, 1, "concept is missing YAML frontmatter")
            else:
                check_frontmatter_fields(meta, md, rel, r)
        check_text(md, bundle, text, rel, r, ste)
    return r


def main() -> int:
    ap = argparse.ArgumentParser(description="Validate a strict OKF v0.2 bundle.")
    ap.add_argument("bundle", nargs="?", default=str(DEFAULT_BUNDLE))
    ap.add_argument("--ste", action="store_true", help="print sentence-length warnings")
    args = ap.parse_args()
    r = validate(pathlib.Path(args.bundle), args.ste)
    for w in r.warnings:
        print(f"warning: {w}")
    for e in r.errors:
        print(f"error: {e}")
    print(f"okf validator {VALIDATOR_VERSION}: {len(r.errors)} error(s), {len(r.warnings)} warning(s)")
    return 1 if r.errors else 0


if __name__ == "__main__":
    sys.exit(main())
