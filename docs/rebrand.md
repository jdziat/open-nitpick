# The name: nitpick

The project was called open-nitpick. It is now called nitpick. This page records
what changed, what did not, and why, so a later change does not undo a
decision that was made on purpose.

## Changed

- Documentation, READMEs, the site, the plugin and marketplace metadata,
  `action.yml`, CLI help and error text, and code comments say nitpick.
- The default mention is `@nitpick-review`, the name of the GitHub App. A bare
  `@nitpick` is another person's account and must not be a default.
- `@open-nitpick` still works for one release, only while `review.mention` is
  the default. An operator who set their own handle did not ask for it.
- The marketplace is named `nitpick`, so the install command is
  `/plugin install nitpick@nitpick`. Add the marketplace again after upgrading.

## Kept

- The hidden markers in pull request comments, such as `<!-- open-nitpick head:... -->`.
  Comments already posted carry them, and the reviewer finds its own comments,
  its progress and its fingerprints by them. A rename would make every open
  pull request look unreviewed.
- The keystore service name `open-nitpick`. A stored API key is found by it, and
  a rename would lose every key `nitpick auth set` saved.
- The model prompts, and any text the review puts into a model's input, such as
  the placeholder that replaces a forged boundary marker. Prompts are measured, so
  rewording them would move results that `docs/findings.md` reports. The notary
  hashes the input, so changed text would file a review under a different hash
  than the record made from it.
- Paths that name where data already lives, such as the bakeoff's frozen run
  directory under `~/.local/share/open-nitpick/`. Renaming the string in a script
  does not move the directory.
- `LICENSE`, `NOTICE` and `CHANGELOG.md`: the copyright holder and release
  history keep the name they were written under.

## Name in prose

Lowercase `nitpick`, as in the command. One `Open-NitPick` spelling was
corrected to match.
