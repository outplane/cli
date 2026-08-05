#!/bin/sh
#
# Installs the Out Plane CLI on macOS and Linux.
#
#   curl -fsSL https://outplane.com/install.sh | sh
#
# Windows is served by npm instead: `npm install -g outplane`. This is a shell
# script, and a PowerShell twin would be a second thing to keep correct for a
# platform that already has a working channel.
#
# What this does, in order: work out which binary this machine needs, find the
# newest release, download it with its checksum, verify it, and move it into
# place. It asks for nothing, writes nothing outside the install directory, and
# leaves no receipt: `outplane update` recognises an installation by where the
# binary sits, so there is no state here to go stale.
#
# Written for POSIX sh rather than bash, because /bin/sh is dash on Debian and
# Ubuntu and this is piped into whatever the machine has.

set -eu

REPO="outplane/cli"
BINARY="outplane"

# Overridable, and both are read by people rather than by us:
#   OUTPLANE_VERSION      install a particular release, such as v0.2.0
#   OUTPLANE_INSTALL_DIR  put the binary somewhere specific
VERSION="${OUTPLANE_VERSION:-}"
INSTALL_DIR="${OUTPLANE_INSTALL_DIR:-}"

say() { printf '%s\n' "$*"; }

# Failures go to stderr, so that a pipeline capturing stdout still shows them.
die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || die "$1 is required and was not found"
}

# ---------------------------------------------------------------- platform

# target names the release asset this machine needs.
#
# The names are what Go calls them, because that is what the release is built
# with. uname says something different on nearly every machine, so the mapping
# is explicit rather than clever.
target() {
	os=$(uname -s)
	arch=$(uname -m)

	case "$os" in
	Darwin) os="darwin" ;;
	Linux) os="linux" ;;
	*) die "$os is not supported. On Windows, install with: npm install -g outplane" ;;
	esac

	case "$arch" in
	x86_64 | amd64) arch="amd64" ;;
	arm64 | aarch64) arch="arm64" ;;
	*) die "$arch is not supported. The CLI ships for amd64 and arm64" ;;
	esac

	printf '%s_%s' "$os" "$arch"
}

# ---------------------------------------------------------------- version

# latest asks GitHub which release is newest, without parsing JSON.
#
# The /releases/latest page redirects to the release's own URL, and the tag is
# the last path segment. That needs no jq, no API token, and no rate limit worth
# worrying about.
latest() {
	url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") ||
		die "could not reach GitHub to find the newest release"

	tag=${url##*/}
	# "releases" is what the last segment is when GitHub did not redirect,
	# which is what a repository with no release at all looks like.
	if [ -z "$tag" ] || [ "$tag" = "releases" ]; then
		die "could not work out the newest version"
	fi
	printf '%s' "$tag"
}

# ---------------------------------------------------------------- install

# writable reports whether a directory can be written to as this user, without
# asking for a password to find out.
writable() {
	[ -d "$1" ] && [ -w "$1" ]
}

# where decides the install directory.
#
# /usr/local/bin when this user can write there, which covers a Mac and most
# development machines. Otherwise ~/.local/bin, which needs no password and is
# on PATH by default on most Linux distributions. sudo is never invoked: a
# script that escalates by itself, having been piped from the internet, is
# asking for a trust nobody offered it.
where() {
	if [ -n "$INSTALL_DIR" ]; then
		printf '%s' "$INSTALL_DIR"
		return
	fi
	if writable /usr/local/bin; then
		printf '%s' /usr/local/bin
		return
	fi
	printf '%s' "$HOME/.local/bin"
}

# verify checks the download against the release's own checksum file.
#
# A truncated download and a tampered one look identical to tar, and this is a
# binary that is about to be run. macOS ships shasum, Linux ships sha256sum, and
# a machine with neither is one where this cannot be checked at all.
verify() {
	archive=$1
	sums=$2
	name=$3

	expected=$(grep " $name\$" "$sums" | cut -d' ' -f1) ||
		die "the checksum file does not mention $name"
	[ -n "$expected" ] || die "the checksum file does not mention $name"

	if command -v sha256sum >/dev/null 2>&1; then
		actual=$(sha256sum "$archive" | cut -d' ' -f1)
	elif command -v shasum >/dev/null 2>&1; then
		actual=$(shasum -a 256 "$archive" | cut -d' ' -f1)
	else
		die "neither sha256sum nor shasum is available, so the download cannot be verified"
	fi

	[ "$actual" = "$expected" ] ||
		die "the download does not match its checksum. Expected $expected, got $actual"
}

main() {
	need curl
	need tar

	platform=$(target)
	[ -n "$VERSION" ] || VERSION=$(latest)

	name="${BINARY}_${VERSION}_${platform}.tar.gz"
	base="https://github.com/$REPO/releases/download/$VERSION"

	say "Installing $BINARY $VERSION for $platform"

	tmp=$(mktemp -d)
	# Removed however this exits, including every die() below.
	trap 'rm -rf "$tmp"' EXIT

	curl -fsSL "$base/$name" -o "$tmp/$name" ||
		die "could not download $name. Is $VERSION a released version?"
	curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt" ||
		die "could not download the checksums for $VERSION"

	verify "$tmp/$name" "$tmp/checksums.txt" "$name"

	tar -xzf "$tmp/$name" -C "$tmp" || die "the archive could not be opened"
	[ -f "$tmp/$BINARY" ] || die "the archive did not contain $BINARY"

	dir=$(where)
	mkdir -p "$dir" || die "could not create $dir"
	chmod +x "$tmp/$BINARY"

	# Moved rather than copied over. Writing to a binary that is currently
	# running fails with "text file busy" on Linux; replacing the directory
	# entry does not, which is what makes `outplane update` able to replace the
	# very binary that is running it.
	mv -f "$tmp/$BINARY" "$dir/$BINARY" ||
		die "could not write to $dir. Set OUTPLANE_INSTALL_DIR to somewhere writable"

	say "Installed $dir/$BINARY"

	case ":$PATH:" in
	*":$dir:"*) ;;
	*)
		say ""
		say "$dir is not on your PATH. Add this to your shell profile:"
		say "  export PATH=\"$dir:\$PATH\""
		;;
	esac

	# Teach whatever coding agent is on this machine, by default.
	#
	# The skill is what makes an agent use this CLI correctly rather than guess
	# at it, and somebody who has just installed the CLI is exactly the person
	# who wants it. It is a suggestion away from being missed entirely, so it
	# happens here, and OUTPLANE_SKIP_SKILLS turns it off for anyone who would
	# rather their editor's configuration were left alone.
	#
	# A failure is reported and then forgotten: the CLI is installed either way,
	# and an install script that fails at the last step over an optional extra
	# has told the user their install failed when it did not.
	# Teach whatever coding agent is on this machine, by default.
	#
	# The skill is what makes an agent use this CLI correctly rather than guess
	# at it, and somebody who has just installed the CLI is exactly the person
	# who wants it. `outplane update` re-runs this script, so the skill is
	# brought to the current release every time the CLI is, which is the point:
	# a skill a release behind describes a CLI that is no longer there.
	#
	# OUTPLANE_SKIP_SKILLS turns it off for anyone who would rather their
	# editor's configuration were left alone.
	#
	# A failure is reported and then forgotten: the CLI is installed either way,
	# and an install script that fails at the last step over an optional extra
	# has told the user their install failed when it did not.
	skills_note=""
	if [ -z "${OUTPLANE_SKIP_SKILLS:-}" ]; then
		if "$dir/$BINARY" skills install >/dev/null 2>&1; then
			skills_note="installed"
		else
			skills_note="skipped"
		fi
	fi

	case "$skills_note" in
	installed)
		say ""
		say "Taught your coding agent to use Out Plane. Restart it to load the skill."
		;;
	skipped)
		say ""
		say "Using a coding agent? Teach it Out Plane: $BINARY skills install"
		;;
	esac

	say ""
	say "Next: $BINARY login"
}

main
