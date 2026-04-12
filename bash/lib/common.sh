#!/usr/bin/env bash
# common.sh — Shared constants, paths, and utilities for Endless

# Resolve the root of the endless installation
ENDLESS_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# XDG-compliant config directory
ENDLESS_CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/endless"
ENDLESS_DB="$ENDLESS_CONFIG_DIR/endless.db"
ENDLESS_CONFIG="$ENDLESS_CONFIG_DIR/config.json"

# Colors (only when stdout is a terminal)
if [[ -t 1 ]]; then
    C_RESET=$'\033[0m'
    C_BOLD=$'\033[1m'
    C_DIM=$'\033[2m'
    C_RED=$'\033[31m'
    C_GREEN=$'\033[32m'
    C_YELLOW=$'\033[33m'
    C_BLUE=$'\033[34m'
    C_CYAN=$'\033[36m'
else
    C_RESET='' C_BOLD='' C_DIM='' C_RED='' C_GREEN='' C_YELLOW='' C_BLUE='' C_CYAN=''
fi

# Print an error message to stderr and exit
die() {
    printf "${C_RED}error:${C_RESET} %s\n" "$*" >&2
    exit 1
}

# Print a warning to stderr
warn() {
    printf "${C_YELLOW}warning:${C_RESET} %s\n" "$*" >&2
}

# Print an info message
info() {
    printf "${C_CYAN}•${C_RESET} %s\n" "$*"
}

# Check for required external tools
check_deps() {
    if ! command -v sqlite3 &>/dev/null; then
        die "sqlite3 is required but not found"
    fi
    if ! command -v jq &>/dev/null; then
        die "jq is required but not found. Install with: brew install jq"
    fi
}

# Ensure the config directory exists
ensure_config_dir() {
    mkdir -p "$ENDLESS_CONFIG_DIR"
}

# Source the other library files
# shellcheck source=lib/db.sh
source "$ENDLESS_ROOT/lib/db.sh"
# shellcheck source=lib/config.sh
source "$ENDLESS_ROOT/lib/config.sh"
