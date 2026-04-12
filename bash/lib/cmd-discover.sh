#!/usr/bin/env bash
# cmd-discover.sh — Discover unregistered projects in configured roots

# Tab character for building TSV data
_TAB=$'\t'

# Get the newest file mtime in a directory (seconds since epoch)
# Skips .git/ contents for accuracy
_newest_mtime() {
    local dir="$1"
    # macOS find doesn't support -printf; use stat
    find "${dir}" -maxdepth 2 \
        -not -path '*/.git/*' \
        -not -path '*/node_modules/*' \
        -not -path '*/vendor/*' \
        -type f 2>/dev/null \
        | head -50 \
        | xargs stat -f '%m' 2>/dev/null \
        | sort -rn \
        | head -1
}

# Format seconds-since-epoch as a human-readable "N days/months ago"
_format_age() {
    local mtime="$1"
    if [[ -z "${mtime}" ]]; then
        echo "unknown"
        return
    fi
    local now
    now=$(date +%s)
    local diff=$((now - mtime))
    local days=$((diff / 86400))

    if [[ "${days}" -lt 1 ]]; then
        echo "today"
    elif [[ "${days}" -eq 1 ]]; then
        echo "1 day ago"
    elif [[ "${days}" -lt 30 ]]; then
        echo "${days} days ago"
    elif [[ "${days}" -lt 365 ]]; then
        local months=$((days / 30))
        if [[ "${months}" -eq 1 ]]; then
            echo "1 month ago"
        else
            echo "${months} months ago"
        fi
    else
        local years=$((days / 365))
        if [[ "${years}" -eq 1 ]]; then
            echo "1 year ago"
        else
            echo "${years} years ago"
        fi
    fi
}

# Detect signals for a directory
# Sets these variables in the caller's scope:
#   _sig_claude_dir, _sig_claude_md, _sig_agents_md
#   _sig_git, _sig_lang_file, _sig_build_file
#   _sig_readme, _sig_language, _sig_mtime, _sig_age
#   _sig_desc (human-readable signal summary)
_detect_signals() {
    local dir="$1"

    _sig_claude_dir=0
    _sig_claude_md=0
    _sig_agents_md=0
    _sig_git=0
    _sig_lang_file=0
    _sig_build_file=0
    _sig_readme=0
    _sig_language=""
    _sig_mtime=""
    _sig_age="unknown"
    _sig_desc=""

    # AI signals
    [[ -d "${dir}/.claude" ]] && _sig_claude_dir=1
    [[ -f "${dir}/CLAUDE.md" ]] && _sig_claude_md=1
    [[ -f "${dir}/AGENTS.md" \
        || -d "${dir}/.codex" ]] && _sig_agents_md=1

    # Git
    [[ -d "${dir}/.git" ]] && _sig_git=1

    # Language files
    if [[ -f "${dir}/go.mod" ]]; then
        _sig_lang_file=1; _sig_language="go"
    elif [[ -f "${dir}/package.json" ]]; then
        _sig_lang_file=1; _sig_language="javascript"
    elif [[ -f "${dir}/Cargo.toml" ]]; then
        _sig_lang_file=1; _sig_language="rust"
    elif [[ -f "${dir}/pyproject.toml" \
        || -f "${dir}/setup.py" ]]; then
        _sig_lang_file=1; _sig_language="python"
    fi

    # Build files
    [[ -f "${dir}/Makefile" \
        || -f "${dir}/justfile" ]] && _sig_build_file=1

    # README
    [[ -f "${dir}/README.md" ]] && _sig_readme=1

    # Fall back to extension-based detection if no lang file
    if [[ -z "${_sig_language}" ]]; then
        _sig_language=$(_detect_language "${dir}")
    fi

    # Recency
    _sig_mtime=$(_newest_mtime "${dir}")
    if [[ -n "${_sig_mtime}" ]]; then
        _sig_age=$(_format_age "${_sig_mtime}")
    fi

    # Build description
    local parts=()
    [[ "${_sig_claude_dir}" -eq 1 ]] && parts+=(".claude")
    [[ "${_sig_claude_md}" -eq 1 ]] && parts+=("CLAUDE.md")
    [[ "${_sig_agents_md}" -eq 1 ]] && parts+=("AGENTS.md")
    [[ "${_sig_git}" -eq 1 ]] && parts+=(".git")
    [[ "${_sig_lang_file}" -eq 1 ]] && parts+=("lang")
    [[ "${_sig_build_file}" -eq 1 ]] && parts+=("build")

    local IFS="+"
    _sig_desc="${parts[*]:-none}"
}

# Classify a directory into a tier (1-5)
_classify_tier() {
    local mtime="${_sig_mtime}"
    local now
    now=$(date +%s)

    local days_old=9999
    if [[ -n "${mtime}" ]]; then
        days_old=$(( (now - mtime) / 86400 ))
    fi

    local has_ai=0
    if [[ "${_sig_claude_dir}" -eq 1 \
        || "${_sig_claude_md}" -eq 1 \
        || "${_sig_agents_md}" -eq 1 ]]; then
        has_ai=1
    fi

    if [[ "${has_ai}" -eq 1 \
        && "${days_old}" -le 90 ]]; then
        echo 1  # Active AI project
    elif [[ "${has_ai}" -eq 1 ]]; then
        echo 2  # AI-configured but dormant
    elif [[ "${_sig_git}" -eq 1 \
        && "${_sig_lang_file}" -eq 1 \
        && "${days_old}" -le 365 ]]; then
        echo 3  # Active dev project
    elif [[ "${_sig_git}" -eq 1 ]]; then
        echo 4  # Dormant project
    else
        echo 5  # Not a project
    fi
}

# Count subdirectories that have .git (to detect groups)
_count_git_subdirs() {
    local dir="$1"
    local count=0
    local subdir
    for subdir in "${dir}"/*/; do
        [[ -d "${subdir}.git" ]] && count=$((count + 1))
    done
    echo "${count}"
}

# Prompt for tier-level decision
# Returns: "all", "skip", "each"
_prompt_tier_action() {
    printf "  ${C_BOLD}[Y]${C_RESET}es all  " >&2
    printf "${C_BOLD}[n]${C_RESET}o/skip  " >&2
    printf "${C_BOLD}[r]${C_RESET}eview each: " >&2
    local input
    read -r input
    case "${input}" in
        ""|Y|y|yes)  echo "all" ;;
        n|N|no)      echo "skip" ;;
        r|R|review)  echo "each" ;;
        *)           echo "skip" ;;
    esac
}

# Prompt for per-project decision
# Returns: "yes", "no", "ignore", "skip_rest"
_prompt_project_action() {
    local name="$1"
    printf "  Register ${C_BOLD}%s${C_RESET}? " \
        "${name}" >&2
    printf "[${C_BOLD}Y${C_RESET}/n/\
${C_BOLD}i${C_RESET}gnore/\
${C_BOLD}s${C_RESET}kip rest]: " >&2
    local input
    read -r input
    case "${input}" in
        ""|Y|y|yes)       echo "yes" ;;
        n|N|no)           echo "no" ;;
        i|I|ignore)       echo "ignore" ;;
        s|S|skip*)        echo "skip_rest" ;;
        *)                echo "no" ;;
    esac
}

# Register a discovered project (wraps cmd_register)
_register_discovered() {
    local dir_path="$1"
    local slug
    slug=$(basename "${dir_path}")
    # Suppress the verbose output from register
    cmd_register --infer "${dir_path}" > /dev/null 2>&1
    return $?
}

# Present and process a single tier
# Args: tier_num, tier_label, tier_dirs (newline-separated)
_present_tier() {
    local tier_num="$1"
    local tier_label="$2"
    local tier_data="$3"

    if [[ -z "${tier_data}" ]]; then
        return 0
    fi

    local count
    count=$(echo "${tier_data}" | wc -l | tr -d ' ')

    printf "\n${C_BOLD}--- Tier %s: %s (%s found) ---\
${C_RESET}\n" \
        "${tier_num}" "${tier_label}" "${count}"

    # Display table
    printf "  ${C_DIM}%-22s %-8s %-24s %s${C_RESET}\n" \
        "NAME" "LANG" "SIGNALS" "CHANGED"
    while IFS=$'\t' read -r dir lang signals age; do
        [[ -z "${dir}" ]] && continue
        [[ "${lang}" == "_" ]] && lang=""
        [[ "${signals}" == "_" ]] && signals=""
        local name
        name=$(basename "${dir}")
        printf "  %-22s %-8s %-24s %s\n" \
            "${name}" "${lang:--}" \
            "${signals:--}" "${age}"
    done <<< "${tier_data}"

    printf "\n"

    # Skip tier 4 by default
    if [[ "${tier_num}" -eq 4 ]]; then
        printf "  ${C_DIM}Skipped by default. \
Use --all to review.${C_RESET}\n"
        return 0
    fi

    local action
    action=$(_prompt_tier_action)

    local registered=0
    case "${action}" in
        all)
            while IFS=$'\t' read -r dir lang signals age; do
                [[ -z "${dir}" ]] && continue
                if _register_discovered "${dir}"; then
                    registered=$((registered + 1))
                fi
            done <<< "${tier_data}"
            info "Registered ${registered} project(s)"
            ;;
        each)
            while IFS=$'\t' read -r dir lang signals age; do
                [[ -z "${dir}" ]] && continue
                [[ "${lang}" == "_" ]] && lang=""
                [[ "${signals}" == "_" ]] && signals=""
                local name
                name=$(basename "${dir}")
                local decision
                decision=$(_prompt_project_action \
                    "${name} (${lang:--}, ${signals:--}, \
${age})")
                case "${decision}" in
                    yes)
                        if _register_discovered "${dir}"; then
                            registered=$((registered + 1))
                        fi
                        ;;
                    ignore)
                        config_add_ignore "${dir}"
                        info "Ignored ${C_BOLD}${name}\
${C_RESET} (added to ignore list)"
                        ;;
                    skip_rest)
                        break
                        ;;
                esac
            done
            info "Registered ${registered} project(s)"
            ;;
        skip)
            info "Skipped tier ${tier_num}"
            ;;
    esac

    _TOTAL_REGISTERED=$((_TOTAL_REGISTERED + registered))
}

# Present discovered groups and offer to mark them
# Populates _CONFIRMED_GROUPS with accepted group dirs
_present_groups() {
    local group_data="$1"
    _CONFIRMED_GROUPS=""
    if [[ -z "${group_data}" ]]; then
        return 0
    fi

    local count
    count=$(echo "${group_data}" | wc -l | tr -d ' ')

    printf "\n${C_BOLD}Found %s group directory(s):\
${C_RESET}\n" "${count}"
    while IFS=$'\t' read -r dir subcount; do
        [[ -z "${dir}" ]] && continue
        local name
        name=$(basename "${dir}")
        printf "  %-22s (%s projects)\n" \
            "${name}" "${subcount}"
    done <<< "${group_data}"

    printf "\n"
    printf "  ${C_BOLD}[Y]${C_RESET}es all  " >&2
    printf "${C_BOLD}[n]${C_RESET}o/skip  " >&2
    printf "${C_BOLD}[r]${C_RESET}eview each: " >&2
    local input
    read -r input

    case "${input}" in
        ""|Y|y|yes)
            while IFS=$'\t' read -r dir subcount; do
                [[ -z "${dir}" ]] && continue
                local name
                name=$(basename "${dir}")
                _CONFIRMED_GROUPS="${_CONFIRMED_GROUPS}${dir}
"
                info "Marked ${C_BOLD}${name}${C_RESET} \
as a project group"
            done <<< "${group_data}"
            ;;
        r|R|review)
            # Read group data into arrays so stdin
            # stays connected to the terminal
            local -a _grp_dirs=()
            local -a _grp_counts=()
            while IFS=$'\t' read -r dir subcount; do
                [[ -z "${dir}" ]] && continue
                _grp_dirs+=("${dir}")
                _grp_counts+=("${subcount}")
            done <<< "${group_data}"

            local _gi=0
            while [[ "${_gi}" -lt "${#_grp_dirs[@]}" ]]; do
                local gdir="${_grp_dirs[${_gi}]}"
                local gcount="${_grp_counts[${_gi}]}"
                local gname
                gname=$(basename "${gdir}")
                _gi=$((_gi + 1))

                printf "  ${C_BOLD}%s${C_RESET} \
(%s projects) — " "${gname}" "${gcount}" >&2
                printf "[${C_BOLD}Y${C_RESET}/n/\
${C_BOLD}i${C_RESET}gnore]: " >&2
                local choice
                read -r choice
                case "${choice}" in
                    ""|Y|y|yes)
                        _CONFIRMED_GROUPS="\
${_CONFIRMED_GROUPS}${gdir}
"
                        info "Marked \
${C_BOLD}${gname}${C_RESET} as a project group"
                        ;;
                    i|I|ignore)
                        config_add_ignore "${gdir}"
                        info "Ignored \
${C_BOLD}${gname}${C_RESET} \
(added to ignore list)"
                        ;;
                    *)
                        info "Skipped \
${C_BOLD}${gname}${C_RESET}"
                        ;;
                esac
            done
            ;;
        *)
            info "Skipped all groups"
            ;;
    esac
}

cmd_discover() {
    local discover_path=""
    local opt_all=0

    # Parse arguments
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --all)  opt_all=1; shift ;;
            -h|--help)
                cat <<'EOF'
Usage: endless discover [<path>] [OPTIONS]

Discover unregistered projects and register them interactively.

Arguments:
  <path>    Directory to scan (default: configured roots)

Options:
  --all     Include dormant projects (tier 4) in review
  -h, --help Show this help
EOF
                return 0
                ;;
            -*)  die "Unknown option: $1" ;;
            *)   discover_path="$1"; shift ;;
        esac
    done

    db_init

    # Determine roots to scan
    local roots=()
    if [[ -n "${discover_path}" ]]; then
        local resolved
        resolved=$(cd "${discover_path}" 2>/dev/null \
            && pwd) \
            || die "Directory not found: ${discover_path}"
        roots+=("${resolved}")
    else
        while IFS= read -r root; do
            if [[ -d "${root}" ]]; then
                roots+=("${root}")
            else
                warn "Root not found: ${root}"
            fi
        done < <(config_roots)
    fi

    if [[ ${#roots[@]} -eq 0 ]]; then
        die "No roots to scan"
    fi

    info "Scanning for unregistered projects..."

    # Collect data: groups, and per-tier project lists
    local group_data=""
    local tier1_data="" tier2_data=""
    local tier3_data="" tier4_data=""
    local skipped=0

    local root
    for root in "${roots[@]}"; do
        local dir
        for dir in "${root}"/*/; do
            [[ ! -d "${dir}" ]] && continue
            # Remove trailing slash
            dir="${dir%/}"
            local name
            name=$(basename "${dir}")

            # Skip hidden directories
            [[ "${name}" == .* ]] && continue

            # Skip ignored directories
            if config_is_ignored "${dir}"; then
                continue
            fi

            # Check if it's a group directory
            local git_subdir_count
            git_subdir_count=$(_count_git_subdirs "${dir}")
            if [[ "${git_subdir_count}" -ge 2 ]]; then
                group_data="${group_data}${dir}${_TAB}${git_subdir_count}
"
                # Also scan children of group dirs
                local subdir
                for subdir in "${dir}"/*/; do
                    [[ ! -d "${subdir}" ]] && continue
                    subdir="${subdir%/}"
                    local subname
                    subname=$(basename "${subdir}")
                    [[ "${subname}" == .* ]] && continue

                    # Skip ignored directories
                    if config_is_ignored "${subdir}"; then
                        continue
                    fi

                    # Skip if already registered
                    if db_exists \
                        "SELECT 1 FROM projects \
WHERE path = '$(_sql_escape "${subdir}")'"; then
                        continue
                    fi

                    _detect_signals "${subdir}"
                    local tier
                    tier=$(_classify_tier)

                    local entry="${subdir}${_TAB}${_sig_language:-_}${_TAB}${_sig_desc:-_}${_TAB}${_sig_age}"
                    case "${tier}" in
                        1) tier1_data="${tier1_data}${entry}
" ;;
                        2) tier2_data="${tier2_data}${entry}
" ;;
                        3) tier3_data="${tier3_data}${entry}
" ;;
                        4) tier4_data="${tier4_data}${entry}
" ;;
                        5) skipped=$((skipped + 1)) ;;
                    esac
                done
                continue
            fi

            # Skip if already registered
            if db_exists \
                "SELECT 1 FROM projects \
WHERE path = '$(_sql_escape "${dir}")'"; then
                continue
            fi

            _detect_signals "${dir}"
            local tier
            tier=$(_classify_tier)

            local entry="${dir}${_TAB}${_sig_language:-_}${_TAB}${_sig_desc:-_}${_TAB}${_sig_age}"
            case "${tier}" in
                1) tier1_data="${tier1_data}${entry}
" ;;
                2) tier2_data="${tier2_data}${entry}
" ;;
                3) tier3_data="${tier3_data}${entry}
" ;;
                4) tier4_data="${tier4_data}${entry}
" ;;
                5) skipped=$((skipped + 1)) ;;
            esac
        done
    done

    # Trim trailing newlines
    group_data=$(echo "${group_data}" | sed '/^$/d')
    tier1_data=$(echo "${tier1_data}" | sed '/^$/d')
    tier2_data=$(echo "${tier2_data}" | sed '/^$/d')
    tier3_data=$(echo "${tier3_data}" | sed '/^$/d')
    tier4_data=$(echo "${tier4_data}" | sed '/^$/d')

    # Check if anything was found
    if [[ -z "${group_data}" \
        && -z "${tier1_data}" \
        && -z "${tier2_data}" \
        && -z "${tier3_data}" \
        && -z "${tier4_data}" ]]; then
        info "No unregistered projects found."
        return 0
    fi

    _TOTAL_REGISTERED=0

    # Present groups
    _present_groups "${group_data}"

    # Present tiers
    _present_tier 1 "Active AI Projects" "${tier1_data}"
    _present_tier 2 "AI-Configured" "${tier2_data}"
    _present_tier 3 "Active Dev Projects" "${tier3_data}"
    if [[ "${opt_all}" -eq 1 ]]; then
        _present_tier 4 "Dormant Projects" "${tier4_data}"
    elif [[ -n "${tier4_data}" ]]; then
        local dormant_count
        dormant_count=$(echo "${tier4_data}" \
            | wc -l | tr -d ' ')
        printf "\n${C_DIM}%s dormant project(s) skipped. \
Use --all to review.${C_RESET}\n" "${dormant_count}"
    fi

    # Summary
    printf "\n${C_BOLD}Summary:${C_RESET} \
Registered ${_TOTAL_REGISTERED} project(s)"
    if [[ "${skipped}" -gt 0 ]]; then
        printf ", skipped ${skipped} non-project dir(s)"
    fi
    printf "\n"
}
