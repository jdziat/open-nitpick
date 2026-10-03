#!/usr/bin/env python3
"""Fail when a built page's View source button targets a repository path that does not exist.

The theme builds that button from edit_uri and the staged page path, so a page
staged under a name it does not carry in the repository (SECURITY.md as
docs/security.md) points at a file GitHub cannot find. The website hook
rewrites those; this scans the button on every built page, so a rewrite the
hook missed or a page staged without one fails the build.

Only the theme's button is checked. Repository links in prose also point at
blob/main/ paths, and a 404 there is a content bug, not a staging bug.
"""
import re
import sys
from pathlib import Path

root = Path(__file__).resolve().parent.parent
site = root / 'public'
# The button the theme renders next to the page title, e.g.
# <a href="https://github.com/o/r/raw/main/docs/guide.md" title="View source of this page" ...>
button = re.compile(r'<a\s+href="(https://github\.com/jdziat/open-nitpick/(?:raw|blob)/main/([^"#?]*))"[^>]*title="View source of this page"')

checked = 0
missing = []
pages = sorted(site.rglob('index.html'))
for page in pages:
    for match in button.finditer(page.read_text()):
        checked += 1
        target = match.group(2)
        if not (root / target).exists():
            missing.append(f'{page.relative_to(root)}: View source -> {target} (not in the repository)')

if missing:
    sys.exit('\n'.join(missing))
if not checked:
    sys.exit('no View source buttons found; the site build or edit_uri changed')
if checked < len(pages):
    sys.exit(f'{checked} View source buttons over {len(pages)} pages; every built page should carry one')
print(f'View source: {checked} buttons resolve across {len(pages)} pages')
