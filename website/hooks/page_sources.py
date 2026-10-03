"""Map staged pages to their repository source files for View source."""

# The staged path each page came from, relative to the repository root.
SOURCES = {
    "index.md": "website/index.md",
    "docs/security.md": "SECURITY.md",
}


def on_pre_page(page, config, files):
    """Rewrite edit_url for a page whose source path is not its site path."""
    source = SOURCES.get(page.file.src_uri)
    if source:
        page.edit_url = f"{config.repo_url}/{config.edit_uri}{source}"
    return page
