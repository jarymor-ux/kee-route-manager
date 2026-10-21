#!/usr/bin/env python3
"""Product documentation must describe the retained router family consistently."""
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parent.parent
errors = []
readmes = [ROOT / name for name in ("README.md", "README.ru.md", "README.zh-CN.md")]
docs = readmes + sorted((ROOT / "docs").glob("*.md")) + [ROOT / "AGENTS.md"]
for path in readmes:
    text = path.read_text()
    for required in ("Keenetic", "Netcraze", "docs/HARDWARE.md"):
        if required not in text:
            errors.append(f"{path.name}: missing scope reference {required}")
retired_paths = re.compile(r"(?:bootstrap-(?:openwrt|linux)\.sh|configs/(?:openwrt|linux-systemd|ui-linux-openwrt)\.yaml|install/(?:openwrt|linux-systemd)/)")
for path in docs:
    text = path.read_text()
    for number, line in enumerate(text.splitlines(), 1):
        if retired_paths.search(line):
            errors.append(f"{path.relative_to(ROOT)}:{number}: retired installation path")
    for match in re.finditer(r"\[[^\]]*\]\(([^)\s]+)(?:\s+[^)]*)?\)", text):
        target = match.group(1).split("#", 1)[0]
        if not target or "://" in target or target.startswith("mailto:"):
            continue
        if not (path.parent / target).exists():
            errors.append(f"{path.relative_to(ROOT)}: broken local link {target}")
hardware = ROOT / "docs/HARDWARE.md"
if not hardware.exists():
    errors.append("missing maintained hardware catalogue")
else:
    text = hardware.read_text()
    for required in ("KN-2410", "KN-2510", "KN-1010", "KN-1011", "KN-1012", "KN-1810", "KN-1811", "NC-1812", "KN-1910", "KN-1912", "KN-3810", "KN-3811", "KN-3812", "KN-2710", "KN-2610", "Hardware verification pending", "GOMIPS=softfloat"):
        if required not in text:
            errors.append(f"HARDWARE.md: missing {required}")
if errors:
    print("\n".join(errors), file=sys.stderr)
    sys.exit(1)
print("Documentation scope and local links: OK")
