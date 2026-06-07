#!/usr/bin/env bash
set -euo pipefail

# install.sh - fetch the latest Backstage release and install the binary.

REPO="This-Is-NPC/backstage"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

get_latest_tag() {
  curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" |
    grep -m1 '"tag_name":' |
    sed -E 's/.*"tag_name" *: *"v?([^"]+)".*/\1/'
}

get_os() {
  case "$(uname -s)" in
    Linux*)  echo Linux ;;
    Darwin*) echo Darwin ;;
    *)       echo "unsupported OS: $(uname -s)" >&2; exit 1 ;;
  esac
}

get_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo x86_64 ;;
    arm64|aarch64) echo arm64 ;;
    *)            echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
  esac
}

ensure_path() {
  command -v backstage >/dev/null 2>&1 && return
  case ":${PATH}:" in
    *":${INSTALL_DIR}:"*) return ;;
  esac
  echo "=> Adding ${INSTALL_DIR} to PATH"
  case "${SHELL:-}" in
    */zsh)  echo "export PATH=\"${INSTALL_DIR}:\${PATH}\"" >> "${HOME}/.zshrc" ;;
    */bash) echo "export PATH=\"${INSTALL_DIR}:\${PATH}\"" >> "${HOME}/.bashrc" ;;
    *)      echo "=> Please add ${INSTALL_DIR} to your PATH manually" ;;
  esac
}

main() {
  local tag="${VERSION:-$(get_latest_tag)}"
  tag="${tag#v}"
  local os; os="$(get_os)"
  local arch; arch="$(get_arch)"
  local asset="backstage_${os}_${arch}.tar.gz"
  local url="https://github.com/${REPO}/releases/download/v${tag}/${asset}"
  local tmpdir; tmpdir="$(mktemp -d)"

  echo "=> Installing backstage v${tag} for ${os} ${arch}..."
  echo "=> Downloading ${url}"

  curl -fsSL "${url}" -o "${tmpdir}/${asset}"
  tar -xzf "${tmpdir}/${asset}" -C "${tmpdir}"

  mkdir -p "${INSTALL_DIR}"
  install -m 755 "${tmpdir}/backstage" "${INSTALL_DIR}/backstage"

  rm -rf "${tmpdir}"

  ensure_path

  echo "=> Installed $("${INSTALL_DIR}/backstage" --version)"
  echo "=> Run 'backstage --help' to get started"
}

main "$@"
