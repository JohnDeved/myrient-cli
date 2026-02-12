#!/usr/bin/env bash
set -euo pipefail

REPO="github.com/JohnDeved/myrient-cli/cmd/myrient@latest"
BIN_NAME="myrient"

if ! command -v go >/dev/null 2>&1; then
  echo "Error: Go is not installed. Install Go first: https://go.dev/doc/install" >&2
  exit 1
fi

echo "Installing ${BIN_NAME} via go install..."
go install "${REPO}"

GOBIN="$(go env GOBIN)"
if [ -z "${GOBIN}" ]; then
  GOPATH="$(go env GOPATH)"
  GOBIN="${GOPATH}/bin"
fi

BIN_PATH="${GOBIN}/${BIN_NAME}"
if [ ! -x "${BIN_PATH}" ]; then
  echo "Error: install completed but binary not found at ${BIN_PATH}" >&2
  exit 1
fi

if command -v "${BIN_NAME}" >/dev/null 2>&1; then
  echo "Installed: $(command -v "${BIN_NAME}")"
  echo "Done. Try: ${BIN_NAME} --help"
  exit 0
fi

if [ -d "${HOME}/bin" ]; then
  ln -sf "${BIN_PATH}" "${HOME}/bin/${BIN_NAME}"
  if command -v "${BIN_NAME}" >/dev/null 2>&1; then
    echo "Linked ${BIN_NAME} into ${HOME}/bin"
    echo "Done. Try: ${BIN_NAME} --help"
    exit 0
  fi
fi

SHELL_NAME="$(basename "${SHELL:-}")"
RC_FILE="${HOME}/.profile"
case "${SHELL_NAME}" in
  zsh) RC_FILE="${HOME}/.zshrc" ;;
  bash) RC_FILE="${HOME}/.bashrc" ;;
esac

EXPORT_LINE='export PATH="$PATH:$(go env GOPATH)/bin"'
if [ -f "${RC_FILE}" ] && grep -Fq "go env GOPATH)/bin" "${RC_FILE}"; then
  echo "${BIN_NAME} installed at ${BIN_PATH}"
  echo "Open a new shell and run: ${BIN_NAME} --help"
  exit 0
fi

echo "${BIN_NAME} installed at ${BIN_PATH}"
echo
echo "It is not on your PATH yet. Add this line to ${RC_FILE}:"
echo "  ${EXPORT_LINE}"
echo
echo "Then run:"
echo "  source ${RC_FILE}"
echo "  ${BIN_NAME} --help"
