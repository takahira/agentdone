#!/usr/bin/env sh
# agentdone installer.
#
# Downloads the single static binary, prompts for a Slack webhook (kept out of
# the repo and the binary), and wires the hooks into ~/.claude/settings.json.
# No jq / Node / Python required.
#
#   curl -fsSL https://raw.githubusercontent.com/<repo>/main/install.sh | sh
set -eu

REPO="${AGENTDONE_REPO:-takahira/agentdone}"

# Resolve the config dir the way the binary does (internal/claudedir.Dir):
# $CLAUDE_CONFIG_DIR when set to a non-empty value (whitespace-trimmed, matching
# the binary's strings.TrimSpace), otherwise ~/.claude. The hooks read the
# webhook from $CLAUDE_CONFIG_DIR/hooks/.webhook, so an installer that hardcoded
# ~/.claude would write the secret where the running hooks never read it — the
# install would "succeed" yet every notification would stay silent.
ccd=$(printf '%s' "${CLAUDE_CONFIG_DIR:-}" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
if [ -n "$ccd" ]; then
	BASE="$ccd"
else
	BASE="$HOME/.claude"
fi
BINDIR="${AGENTDONE_BINDIR:-$BASE/bin}"
BIN="$BINDIR/agentdone"

# AGENTDONE_REPO points the download AND the provenance check (gh attestation
# verify --repo) at a different repo. That is intended for testing a fork's own
# signed build, but it also means you are NOT installing the canonical release —
# make that loud so a stray value in .envrc/CI can't silently swap the source.
if [ "$REPO" != "takahira/agentdone" ]; then
	echo "WARNING: installing from AGENTDONE_REPO=$REPO, not the canonical repo. Provenance checks are scoped to this override." >&2
fi

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
	mingw* | msys* | cygwin*)
		echo "This installer targets macOS/Linux. On Windows, download agentdone_windows_<arch>.zip from https://github.com/$REPO/releases and run: agentdone init" >&2
		exit 1
		;;
esac
arch=$(uname -m)
case "$arch" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) echo "unsupported arch: $arch" >&2 && exit 1 ;;
esac

# Resolve "latest" to a concrete tag FIRST, then download the archive and its
# checksums from that tag. Fetching both via .../latest/... could straddle a
# release being published and pair an old archive with new checksums.
tag="${AGENTDONE_VERSION:-latest}"
if [ "$tag" = latest ]; then
	tag=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" | sed 's#.*/##')
	case "$tag" in
		v*) ;;
		*) echo "could not resolve the latest release tag (got: $tag)" >&2 && exit 1 ;;
	esac
fi
url="https://github.com/$REPO/releases/download/$tag/agentdone_${os}_${arch}.tar.gz"

echo "Downloading $url"
mkdir -p "$BINDIR"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "$url" -o "$tmp/ch.tar.gz"

# Verify the download against the release checksums.txt before extracting/running.
asset="agentdone_${os}_${arch}.tar.gz"
curl -fsSL "${url%/*}/checksums.txt" -o "$tmp/checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/ch.tar.gz" | awk '{print $1}')
else
	got=$(shasum -a 256 "$tmp/ch.tar.gz" | awk '{print $1}')
fi
want=$(awk -v f="$asset" '$2 == f {print $1}' "$tmp/checksums.txt")
if [ -z "$want" ]; then
	echo "checksum for $asset not found in checksums.txt" >&2
	exit 1
fi
if [ "$got" != "$want" ]; then
	echo "checksum mismatch for $asset (got $got, want $want)" >&2
	exit 1
fi

# The checksum is fetched from the same origin as the archive, so it guards
# corruption/truncation but not a tampered release. For an identity-bound
# guarantee, verify the GitHub build-provenance attestation when the `gh` CLI is
# available. Best-effort/non-fatal: attestations may be absent (older/test
# releases) or gh may be unauthenticated, and the checksum already matched.
if command -v gh >/dev/null 2>&1; then
	if gh attestation verify "$tmp/ch.tar.gz" --repo "$REPO" >/dev/null 2>&1; then
		echo "Verified the build-provenance attestation."
	else
		echo "Note: could not verify the build-provenance attestation; proceeding on the checksum match. To check it yourself: gh attestation verify $asset --repo $REPO" >&2
	fi
fi

tar -xzf "$tmp/ch.tar.gz" -C "$tmp"
install -m 0755 "$tmp/agentdone" "$BIN"

# Webhook: stored at $BASE/hooks/.webhook (600), never in the repo/binary.
webhook="$BASE/hooks/.webhook"
if [ ! -f "$webhook" ]; then
	# Under `curl ... | sh`, stdin is the script itself, not the terminal, so a
	# plain `read` would never reach the user (and could even consume the next
	# script line). Prompt on the controlling tty; with none (CI, non-interactive)
	# skip and let the user configure the webhook later.
	hook=""
	if [ -e /dev/tty ] && (exec </dev/tty) 2>/dev/null; then
		printf "Slack Incoming Webhook URL (blank to set later): "
		read -r hook </dev/tty || hook=""
	else
		echo "No terminal to prompt for the webhook; set SLACK_WEBHOOK_URL or write $webhook (chmod 600), then run: agentdone init" >&2
	fi
	if [ -n "$hook" ]; then
		mkdir -p "$(dirname "$webhook")"
		# Create the secret 0600 from the start (umask in a subshell), so it is
		# never briefly world-readable between the write and a later chmod.
		( umask 077; printf '%s' "$hook" >"$webhook" )
		chmod 600 "$webhook"
	fi
fi

"$BIN" init
echo "Installed: $BIN"
# The hooks invoke the binary by absolute path, so PATH only affects manual use
# (agentdone doctor / uninstall). Point it out rather than leave a confusing
# "command not found" for the README's next step.
case ":$PATH:" in
	*":$BINDIR:"*) ;;
	*)
		echo "Note: $BINDIR is not on your PATH. Either add it:"
		echo "  export PATH=\"\$PATH:$BINDIR\""
		echo "or call the binary by full path: $BIN"
		;;
esac
