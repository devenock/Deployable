#!/usr/bin/env bash
# Deployable CLI installer.
#
# Usage:
#   curl -sSL <your-deployable-instance>/install.sh | bash
#
# There's no public binary release channel yet (that lands alongside CI/CD
# in a later phase — see `make release` in the Makefile), so for now this
# builds the CLI from source: it clones the repo (or reuses one you already
# have via DEPLOYABLE_REPO_DIR) and runs `make build-cli`, then installs the
# binary matching your OS/arch onto your PATH.
set -euo pipefail

REPO_URL="${DEPLOYABLE_REPO_URL:-}"
REPO_DIR="${DEPLOYABLE_REPO_DIR:-}"
INSTALL_DIR="${DEPLOYABLE_INSTALL_DIR:-/usr/local/bin}"

os() {
  case "$(uname -s)" in
    Linux)  echo linux ;;
    Darwin) echo darwin ;;
    *) echo "unsupported OS: $(uname -s)" >&2; exit 1 ;;
  esac
}

arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo amd64 ;;
    arm64|aarch64) echo arm64 ;;
    *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
  esac
}

require() {
  command -v "$1" >/dev/null 2>&1 || { echo "error: $1 is required but not found on PATH" >&2; exit 1; }
}

main() {
  require go
  require make

  local os_name arch_name bin_name work_dir cleanup_dir=""
  os_name=$(os)
  arch_name=$(arch)
  bin_name="deployable-${os_name}-${arch_name}"

  if [ -n "$REPO_DIR" ]; then
    work_dir="$REPO_DIR"
  elif [ -f "go.mod" ] && grep -q '^module deployable$' go.mod 2>/dev/null; then
    work_dir="$(pwd)"
  elif [ -n "$REPO_URL" ]; then
    require git
    cleanup_dir=$(mktemp -d)
    echo "Cloning $REPO_URL..."
    git clone --depth 1 "$REPO_URL" "$cleanup_dir"
    work_dir="$cleanup_dir"
  else
    echo "error: no Deployable source found." >&2
    echo "Run this from within a clone of the deployable repo, or set DEPLOYABLE_REPO_URL / DEPLOYABLE_REPO_DIR." >&2
    exit 1
  fi

  echo "Building CLI binaries in $work_dir..."
  (cd "$work_dir" && make build-cli)

  local built_path="$work_dir/dist/$bin_name"
  if [ ! -f "$built_path" ]; then
    echo "error: expected build output not found at $built_path" >&2
    exit 1
  fi

  if [ -w "$INSTALL_DIR" ]; then
    install -m 0755 "$built_path" "$INSTALL_DIR/deployable"
  else
    echo "Installing to $INSTALL_DIR requires sudo:"
    sudo install -m 0755 "$built_path" "$INSTALL_DIR/deployable"
  fi

  [ -n "$cleanup_dir" ] && rm -rf "$cleanup_dir"

  echo "Deployable CLI installed to $INSTALL_DIR/deployable"
  echo "Run 'deployable .' inside any project directory to get started."
}

main "$@"
