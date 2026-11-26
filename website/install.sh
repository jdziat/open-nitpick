#!/bin/sh
# Install the latest nitpick release.
#
#   curl -fsSL https://nitpick-oss.github.io/nitpick/install.sh | sh
#
# It downloads the binary for this machine, checks it against the release's
# checksums.txt, checks the Sigstore signature too when cosign is installed, and
# puts it in a directory you own. It never uses sudo.
#
# Environment:
#   VERSION      the release to install, such as v2.10.0. Default: the latest.
#   INSTALL_DIR  where to put nitpick. Default: $HOME/.local/bin
#
# Read by tests and by anyone mirroring releases:
#   NITPICK_RELEASE_BASE  where assets are served, as <base>/<tag>/<asset>
#   NITPICK_LATEST_BASE   where the latest release's files are served, as
#                         <base>/checksums.txt
#
# Everything is inside main, which runs on the last line. A download cut off
# partway therefore runs nothing at all.

main() {
	set -eu

	repo=nitpick-oss/nitpick
	release_base=${NITPICK_RELEASE_BASE:-https://github.com/$repo/releases/download}
	latest_base=${NITPICK_LATEST_BASE:-https://github.com/$repo/releases/latest/download}

	say() { printf '%s\n' "$*"; }
	die() {
		printf 'install.sh: %s\n' "$*" >&2
		exit 1
	}
	have() { command -v "$1" >/dev/null 2>&1; }

	# The platform, before any network request: an unsupported one must fail
	# without having downloaded anything.
	case "$(uname -s)" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	MINGW* | MSYS* | CYGWIN*) die "this script is for linux and darwin; on Windows run install.ps1" ;;
	*) die "unsupported operating system $(uname -s): releases are built for linux and darwin" ;;
	esac
	case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) die "unsupported architecture $(uname -m): releases are built for amd64 and arm64" ;;
	esac

	if have curl; then
		fetch() { curl -fsSL -o "$2" "$1"; }
	elif have wget; then
		fetch() { wget -q -O "$2" "$1"; }
	else
		die "curl or wget is required"
	fi

	if have sha256sum; then
		sha256() { sha256sum "$1" | awk '{print $1}'; }
	elif have shasum; then
		sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
	else
		die "sha256sum or shasum is required to check the download"
	fi

	if [ -n "${INSTALL_DIR:-}" ]; then
		dir=$INSTALL_DIR
	elif [ -n "${HOME:-}" ]; then
		dir=$HOME/.local/bin
	else
		die "HOME is not set; set INSTALL_DIR to where nitpick should go"
	fi

	tmp=$(mktemp -d 2>/dev/null || mktemp -d -t nitpick-install) || die "cannot make a temporary directory"
	# shellcheck disable=SC2064  # $tmp is expanded now on purpose: it is fixed.
	trap "rm -rf '$tmp'" EXIT HUP INT TERM

	version=${VERSION:-}
	if [ -z "$version" ]; then
		# The latest release is found through its checksums.txt, which lists every
		# asset with its version in the name, and not through the GitHub API:
		# unauthenticated API requests are limited to 60 an hour per address, and
		# CI runners and offices share addresses.
		fetch "$latest_base/checksums.txt" "$tmp/latest-checksums.txt" ||
			die "could not find the latest release; set VERSION=vX.Y.Z to skip the lookup"
		suffix=_${os}_${arch}
		name=$(awk -v suffix="$suffix" '{
			n = $2; sub(/^\*/, "", n)
			if (index(n, "nitpick_v") == 1 && substr(n, length(n) - length(suffix) + 1) == suffix) { print n; exit }
		}' "$tmp/latest-checksums.txt")
		[ -n "$name" ] || die "the latest release has no build for $os/$arch"
		name=${name#nitpick_}
		version=${name%"$suffix"}
	fi
	case $version in
	v*) ;;
	*) version=v$version ;;
	esac
	# The version goes into a URL and a file name, so it is checked, not trusted.
	case $version in
	*[!A-Za-z0-9.+-]*) die "$version is not a release version" ;;
	v[0-9]*) ;;
	*) die "$version is not a release version" ;;
	esac

	asset=nitpick_${version}_${os}_${arch}
	say "Installing nitpick $version ($os/$arch) to $dir"

	fetch "$release_base/$version/$asset" "$tmp/$asset" || die "could not download $asset for $version"
	fetch "$release_base/$version/checksums.txt" "$tmp/checksums.txt" || die "could not download checksums.txt for $version"

	# Compared as text: the listed name must be exactly this asset, so a checksum
	# for some other file cannot stand in for it.
	expected=$(awk -v f="$asset" '$2 == f || $2 == "*" f { print $1 }' "$tmp/checksums.txt" | head -n 1 | tr 'A-F' 'a-f')
	[ -n "$expected" ] || die "checksums.txt does not list $asset; nothing was installed"
	actual=$(sha256 "$tmp/$asset")
	[ "$expected" = "$actual" ] || die "checksum mismatch for $asset (expected $expected, got $actual); nothing was installed"

	if have cosign; then
		fetch "$release_base/$version/$asset.sigstore.json" "$tmp/$asset.sigstore.json" ||
			die "could not download the signature bundle for $asset; nothing was installed"
		cosign verify-blob --bundle "$tmp/$asset.sigstore.json" \
			--certificate-identity-regexp "^https://github.com/$repo/" \
			--certificate-oidc-issuer https://token.actions.githubusercontent.com \
			"$tmp/$asset" >/dev/null ||
			die "signature verification failed for $asset; nothing was installed"
		say "Verified the checksum and the Sigstore signature."
	else
		say "Verified the checksum only. The checksum is published beside the binary, so it"
		say "catches a corrupt download, not a compromised release; install cosign to also"
		say "check the signature."
	fi

	mkdir -p "$dir" 2>/dev/null || die "cannot create $dir; set INSTALL_DIR to a directory you own (this script never uses sudo)"
	[ -w "$dir" ] || die "cannot write to $dir; set INSTALL_DIR to a directory you own (this script never uses sudo)"
	# Written beside the target and renamed into place, so an interrupted install
	# never leaves a half-written nitpick where the old one was.
	if ! { cp "$tmp/$asset" "$dir/.nitpick.$$" && chmod 755 "$dir/.nitpick.$$" && mv -f "$dir/.nitpick.$$" "$dir/nitpick"; }; then
		rm -f "$dir/.nitpick.$$"
		die "could not install into $dir"
	fi

	# Captured first: die inside $( ) would only end that subshell.
	ran=$("$dir/nitpick" version 2>&1) || die "installed, but $dir/nitpick does not run: $ran"
	say "Installed: $ran"
	case ":${PATH:-}:" in
	*":$dir:"*) ;;
	*) say "Add $dir to your PATH, for example: export PATH=\"$dir:\$PATH\"" ;;
	esac
}

main "$@"
