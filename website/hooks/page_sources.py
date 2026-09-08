"""Point the three staged pages at the files they were staged from.

`make docs` copies README.md into the docs directory as guide.md,
website/index.md as index.md and SECURITY.md as docs/security.md, so the site's
own paths are the only ones mkdocs knows. edit_uri is resolved against those,
and the "view source" action on the homepage, the Guide and the security policy
asked GitHub for /index.md, /guide.md and /docs/security.md, none of which
exists in the repository. All three 404ed; the other fifteen pages resolve,
because docs/*.md is staged under the same name it has on disk.
"""

# The staged path each page came from, relative to the repository root.
SOURCES = {
    "index.md": "website/index.md",
    "guide.md": "README.md",
    "docs/security.md": "SECURITY.md",
}


def on_pre_page(page, config, files):
    """Rewrite edit_url for a page whose source path is not its site path."""
    source = SOURCES.get(page.file.src_uri)
    if source:
        page.edit_url = f"{config.repo_url}/{config.edit_uri}{source}"
    return page
