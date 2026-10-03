#!/usr/bin/env python3
"""Compare reference sections and flag tables with the built CLI's help."""
import re
import subprocess
import sys
from pathlib import Path

binary = sys.argv[1]
reference = Path('docs/reference/cli.md').read_text()
headings = list(re.finditer(r'^#{2,3} `([^`]+)`\s*$', reference, re.M))
sections = {
    match[1]: reference[match.end():headings[i + 1].start() if i + 1 < len(headings) else len(reference)]
    for i, match in enumerate(headings)
}
source = Path('cmd/nitpick/main.go').read_text()
commands = set(re.findall(r'^\s*case "([a-z][a-z-]+)"', source, re.M)) - {'help'}
commands.update({'mcp install', 'mcp clients', 'auth set', 'auth delete', 'auth list'})
inherits = {'fast-review': 'review', 'improve': 'review', 'repo-score': 'full-review'}
errors = []
for command in sorted(commands):
    if command not in sections:
        errors.append(f'{command}: missing command heading')
        continue
    # Auth subcommands do not parse flags: asking for their help can read the keystore.
    if command.startswith('auth '):
        continue
    result = subprocess.run([binary, *command.split(), '-h'], stdin=subprocess.DEVNULL,
                            capture_output=True, text=True, timeout=15)
    if result.returncode:
        errors.append(f'{command}: help exited {result.returncode}')
        continue
    flags = set(re.findall(r'^  (-[a-z][a-z0-9-]*)(?=\s|$)', result.stdout + result.stderr, re.M))
    section = sections[command]
    if command in inherits:
        section += sections.get(inherits[command], '')
    tables = [line for line in section.splitlines() if line.startswith('|') and '`-' in line]
    if tables:
        section = '\n'.join(tables)
    documented = set(re.findall(r'`(-[a-z][a-z0-9-]*)(?=[\s`=])', section)) - {'-h'}
    for flag in sorted(flags - documented):
        errors.append(f'{command}: undocumented flag {flag}')
    for flag in sorted(documented - flags):
        errors.append(f'{command}: documented unsupported flag {flag}')
if errors:
    sys.exit('\n'.join(errors))
print(f'CLI reference: {len(commands)} command sections and their flags verified')
