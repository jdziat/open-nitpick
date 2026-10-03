#!/usr/bin/env python3
"""Fail when a built page's View source link targets a repository path that does not exist.

The theme builds that link from edit_uri and the staged page path, so a page
staged under a name it does not carry in the repository (README.md as guide.md,
SECURITY.md as docs/security.md) points at a file GitHub cannot find. The
website hook rewrites those, and this check proves every rewrite landed against
the page the build actually produced.
"""
import re
import sys
from pathlib import Path

root = Path(__file__).resolve().parent.parent
site = root / 'public'
pattern = re.compile(r'href="https://github\.com/jdziat/open-nitpick/(?:raw|blob)/main/([^"#?]*)"')

checked = 0
missing = []
for page in sorted(site.rglob('index.html')):
    for match in pattern.finditer(page.read_text()):
        checked += 1
        target = match.group(1)
        if not (root / target).exists():
            missing.append(f'{page.relative_to(root)}: View source -> {target} (not in the repository)')

if missing:
    sys.exit('\n'.join(missing))
if not checked:
    sys.exit('no View source links found; the site build or edit_uri changed')
print(f'View source: {checked} repository links resolve')
