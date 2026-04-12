#!/usr/bin/env bash
# config.sh — Global config management for Endless (JSON via jq)

# Default global config
_DEFAULT_CONFIG='{
  "roots": ["~/Projects"],
  "scan_interval": 300,
  "ignore": []
}'

# Initialize global config if it doesn't exist
config_init() {
    ensure_config_dir
    if [[ ! -f "$ENDLESS_CONFIG" ]]; then
        echo "$_DEFAULT_CONFIG" | jq '.' > "$ENDLESS_CONFIG"
    fi
}

# Read a value from global config
# Usage: val=$(config_get '.roots')
config_get() {
    config_init
    jq -r "$1" "$ENDLESS_CONFIG"
}

# Write a value to global config
# Usage: config_set '.scan_interval' '600'
config_set() {
    config_init
    local tmp
    tmp=$(mktemp)
    jq "$1 = $2" "$ENDLESS_CONFIG" > "$tmp" && mv "$tmp" "$ENDLESS_CONFIG"
}

# Get expanded roots (resolve ~ to $HOME)
config_roots() {
    config_init
    jq -r '.roots[]' "$ENDLESS_CONFIG" | sed "s|^~|$HOME|"
}

# Check if a path is in the ignore list
# Usage: if config_is_ignored "/path/to/dir"; then ...
config_is_ignored() {
    config_init
    local dir_path="$1"
    local short_path="${dir_path/#${HOME}/\~}"
    # Check both full path and ~-relative path
    local match
    match=$(jq -r \
        --arg p "${dir_path}" \
        --arg s "${short_path}" \
        '.ignore // [] | map(select(. == $p or . == $s)) | length' \
        "${ENDLESS_CONFIG}")
    [[ "${match}" -gt 0 ]]
}

# Add a path to the ignore list
# Usage: config_add_ignore "/path/to/dir"
config_add_ignore() {
    config_init
    local dir_path="$1"
    local short_path="${dir_path/#${HOME}/\~}"
    # Store as ~-relative for readability
    if config_is_ignored "${dir_path}"; then
        return 0
    fi
    local tmp
    tmp=$(mktemp)
    jq --arg p "${short_path}" \
        '.ignore = ((.ignore // []) + [$p] | unique)' \
        "${ENDLESS_CONFIG}" > "${tmp}" \
        && mv "${tmp}" "${ENDLESS_CONFIG}"
}

# Read a value from a project's .endless/config.json
# Usage: val=$(project_config_get /path/to/project '.name')
project_config_get() {
    local project_path="$1" key="$2"
    local config_file="$project_path/.endless/config.json"
    if [[ -f "$config_file" ]]; then
        jq -r "$key" "$config_file"
    fi
}

# Write a project's .endless/config.json
# Usage: project_config_write /path/to/project "$json_content"
project_config_write() {
    local project_path="$1" content="$2"
    mkdir -p "$project_path/.endless"
    echo "$content" | jq '.' > "$project_path/.endless/config.json"
}
