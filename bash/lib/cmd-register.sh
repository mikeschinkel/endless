#!/usr/bin/env bash
# cmd-register.sh — Register a directory as an Endless project

# Validate project slug: lowercase alphanumeric, hyphens, underscores
_validate_slug() {
    local slug="$1"
    if [[ ! "${slug}" =~ ^[a-z0-9][a-z0-9_-]*$ ]]; then
        return 1
    fi
    return 0
}

# Detect primary language from file extensions in a directory
_detect_language() {
    local dir="$1"
    local counts
    counts=$(find "${dir}" -maxdepth 2 -type f \( \
        -name '*.go' -o -name '*.ts' -o -name '*.tsx' \
        -o -name '*.js' -o -name '*.jsx' \
        -o -name '*.py' -o -name '*.rs' -o -name '*.rb' \
        -o -name '*.sh' -o -name '*.bash' \
        \) 2>/dev/null \
        | sed 's/.*\.//' | sort | uniq -c \
        | sort -rn | head -1)

    if [[ -z "${counts}" ]]; then
        echo ""
        return
    fi

    local ext
    ext=$(echo "${counts}" | awk '{print $2}')
    case "${ext}" in
        go)          echo "go" ;;
        ts|tsx)      echo "typescript" ;;
        js|jsx)      echo "javascript" ;;
        py)          echo "python" ;;
        rs)          echo "rust" ;;
        rb)          echo "ruby" ;;
        sh|bash)     echo "bash" ;;
        *)           echo "${ext}" ;;
    esac
}

# Prompt the user for input with a default value
_prompt() {
    local prompt="$1" default="$2" var_name="$3"
    if [[ -n "${default}" ]]; then
        printf "${C_BOLD}%s${C_RESET} [%s]: " \
            "${prompt}" "${default}" >&2
    else
        printf "${C_BOLD}%s${C_RESET}: " "${prompt}" >&2
    fi
    local input
    read -r input
    if [[ -z "${input}" ]]; then
        eval "${var_name}='${default}'"
    else
        eval "${var_name}='${input}'"
    fi
}

# Prompt for selection from a list
_prompt_select() {
    local prompt="$1" default="$2" var_name="$3"
    shift 3
    local options=("$@")

    printf "${C_BOLD}%s${C_RESET} [%s]:\n" \
        "${prompt}" "${default}" >&2
    local i=1
    for opt in "${options[@]}"; do
        printf "  %d) %s\n" "${i}" "${opt}" >&2
        i=$((i + 1))
    done
    printf "Choice: " >&2

    local input
    read -r input
    if [[ -z "${input}" ]]; then
        eval "${var_name}='${default}'"
    elif [[ "${input}" =~ ^[0-9]+$ ]] \
        && [[ "${input}" -ge 1 ]] \
        && [[ "${input}" -le ${#options[@]} ]]; then
        eval "${var_name}='${options[$((input-1))]}'"
    else
        eval "${var_name}='${input}'"
    fi
}

# Check if parent dir might be a group directory
_check_group_suggestion() {
    local project_path="$1"
    local parent_dir
    parent_dir=$(dirname "${project_path}")
    local parent_name
    parent_name=$(basename "${parent_dir}")

    # Don't suggest for the roots themselves
    local root
    while IFS= read -r root; do
        [[ "${parent_dir}" == "${root}" ]] && return 0
    done < <(config_roots)

    # Check if other registered projects share this parent
    local escaped_parent
    escaped_parent=$(_sql_escape "${parent_dir}")
    local escaped_path
    escaped_path=$(_sql_escape "${project_path}")
    local sibling_count
    sibling_count=$(db_scalar \
        "SELECT count(*) FROM projects \
WHERE path LIKE '${escaped_parent}/%' \
AND path != '${escaped_path}'")

    if [[ "${sibling_count}" -gt 0 ]]; then
        local existing_group
        existing_group=$(db_scalar \
            "SELECT group_name FROM projects \
WHERE path LIKE '${escaped_parent}/%' \
AND group_name IS NOT NULL LIMIT 1")
        if [[ -z "${existing_group}" ]]; then
            printf "\n"
            info "Found ${sibling_count} other project(s) \
under ${C_BOLD}${parent_dir}${C_RESET}"
            printf "  Mark ${C_BOLD}%s${C_RESET} as a \
project group? [Y/n]: " "${parent_name}" >&2
            local answer
            read -r answer
            if [[ -z "${answer}" \
                || "${answer}" =~ ^[Yy] ]]; then
                local escaped_pname
                escaped_pname=$(_sql_escape "${parent_name}")
                db_exec "UPDATE projects \
SET group_name = '${escaped_pname}' \
WHERE path LIKE '${escaped_parent}/%'"
                local group_total
                group_total=$((sibling_count + 1))
                info "Marked ${C_BOLD}${parent_name}\
${C_RESET} as a group for ${group_total} projects"
            fi
        fi
    fi
}

# SQL-escape a string (double single quotes)
_sql_escape() {
    echo "${1//\'/\'\'}"
}

cmd_register() {
    local project_path=""
    local opt_slug="" opt_name="" opt_desc=""
    local opt_lang="" opt_status=""
    local opt_infer=0

    # Parse arguments
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --infer)     opt_infer=1; shift ;;
            --slug)      opt_slug="$2"; shift 2 ;;
            --name)      opt_name="$2"; shift 2 ;;
            --desc)      opt_desc="$2"; shift 2 ;;
            --lang)      opt_lang="$2"; shift 2 ;;
            --status)    opt_status="$2"; shift 2 ;;
            -h|--help)
                cat <<'EOF'
Usage: endless register [<path>] [OPTIONS]

Register a directory as an Endless project.

Arguments:
  <path>          Project directory (default: current directory)

Options:
  --infer         Auto-detect metadata, skip prompts
  --slug SLUG     Project identifier (lowercase, hyphens, underscores)
  --name NAME     Display name (freeform)
  --desc DESC     Project description
  --lang LANG     Primary language
  --status STT    Status: active, paused, archived, idea
  -h, --help      Show this help
EOF
                return 0
                ;;
            -*)          die "Unknown option: $1" ;;
            *)           project_path="$1"; shift ;;
        esac
    done

    # Default to current directory
    project_path="${project_path:-${PWD}}"
    project_path=$(cd "${project_path}" 2>/dev/null && pwd) \
        || die "Directory not found: ${project_path}"

    # Check if already registered
    local is_update=0
    local escaped_path
    escaped_path=$(_sql_escape "${project_path}")
    if db_exists \
        "SELECT 1 FROM projects \
WHERE path = '${escaped_path}'"; then
        is_update=1
        info "Project already registered at \
${C_BOLD}${project_path}${C_RESET} — updating"
    fi

    # Determine defaults
    local dir_name
    dir_name=$(basename "${project_path}")
    local detected_lang
    detected_lang=$(_detect_language "${project_path}")

    # If updating, load existing values as defaults
    local existing_slug="" existing_name=""
    local existing_desc="" existing_lang="" existing_status=""
    if [[ "${is_update}" -eq 1 ]]; then
        existing_slug=$(db_scalar \
            "SELECT slug FROM projects \
WHERE path = '${escaped_path}'")
        existing_name=$(db_scalar \
            "SELECT name FROM projects \
WHERE path = '${escaped_path}'")
        existing_desc=$(db_scalar \
            "SELECT description FROM projects \
WHERE path = '${escaped_path}'")
        existing_lang=$(db_scalar \
            "SELECT language FROM projects \
WHERE path = '${escaped_path}'")
        existing_status=$(db_scalar \
            "SELECT status FROM projects \
WHERE path = '${escaped_path}'")
    fi

    local slug name desc lang status

    if [[ "${opt_infer}" -eq 1 ]]; then
        # Auto-infer mode: no prompts
        slug="${opt_slug:-${existing_slug:-${dir_name}}}"
        name="${opt_name:-${existing_name:-}}"
        desc="${opt_desc:-${existing_desc:-}}"
        lang="${opt_lang:-${existing_lang:-${detected_lang}}}"
        status="${opt_status:-${existing_status:-active}}"
    else
        # Interactive mode (flags override prompts)
        if [[ -n "${opt_slug}" ]]; then
            slug="${opt_slug}"
        else
            _prompt "Slug (identifier)" \
                "${existing_slug:-${dir_name}}" slug
        fi

        if [[ -n "${opt_name}" ]]; then
            name="${opt_name}"
        else
            _prompt "Name (display)" \
                "${existing_name:-}" name
        fi

        if [[ -n "${opt_desc}" ]]; then
            desc="${opt_desc}"
        else
            _prompt "Description" \
                "${existing_desc:-}" desc
        fi

        if [[ -n "${opt_lang}" ]]; then
            lang="${opt_lang}"
        else
            _prompt "Language" \
                "${existing_lang:-${detected_lang}}" lang
        fi

        if [[ -n "${opt_status}" ]]; then
            status="${opt_status}"
        else
            _prompt_select "Status" \
                "${existing_status:-active}" status \
                "active" "paused" "archived" "idea"
        fi
    fi

    # Validate slug
    if ! _validate_slug "${slug}"; then
        die "Invalid slug: '${slug}' (must be lowercase \
alphanumeric, hyphens, or underscores)"
    fi

    # Validate status
    case "${status}" in
        active|paused|archived|idea) ;;
        *) die "Invalid status: ${status} \
(must be: active, paused, archived, idea)" ;;
    esac

    # Write .endless/config.json
    local config_json
    config_json=$(jq -n \
        --arg slug "${slug}" \
        --arg name "${name}" \
        --arg desc "${desc}" \
        --arg lang "${lang}" \
        --arg status "${status}" \
        '{
            slug: $slug,
            name: $name,
            description: $desc,
            language: $lang,
            status: $status,
            dependencies: [],
            documents: { rules: [] }
        }')
    project_config_write "${project_path}" "${config_json}"

    # Detect group_name from parent directory
    local group_name=""
    local parent_dir
    parent_dir=$(dirname "${project_path}")
    local is_root=0
    while IFS= read -r root; do
        [[ "${parent_dir}" == "${root}" ]] && is_root=1
    done < <(config_roots)
    if [[ "${is_root}" -eq 0 ]]; then
        group_name=$(basename "${parent_dir}")
    fi

    # Upsert into SQLite
    local now
    now=$(date -u '+%Y-%m-%dT%H:%M:%S')
    local escaped_slug
    escaped_slug=$(_sql_escape "${slug}")
    local escaped_name
    escaped_name=$(_sql_escape "${name}")
    local escaped_desc
    escaped_desc=$(_sql_escape "${desc}")
    local escaped_status
    escaped_status=$(_sql_escape "${status}")
    local escaped_lang
    escaped_lang=$(_sql_escape "${lang}")
    local escaped_group
    if [[ -n "${group_name}" ]]; then
        escaped_group="'$(_sql_escape "${group_name}")'"
    else
        escaped_group="NULL"
    fi

    if [[ "${is_update}" -eq 1 ]]; then
        db_exec "UPDATE projects SET
            slug = '${escaped_slug}',
            name = '${escaped_name}',
            group_name = ${escaped_group},
            description = '${escaped_desc}',
            status = '${escaped_status}',
            language = '${escaped_lang}',
            updated_at = '${now}'
            WHERE path = '${escaped_path}'"
        info "Updated ${C_BOLD}${slug}${C_RESET}"
    else
        db_exec "INSERT INTO projects \
(slug, name, path, group_name, description, \
status, language, created_at, updated_at)
            VALUES (
                '${escaped_slug}',
                '${escaped_name}',
                '${escaped_path}',
                ${escaped_group},
                '${escaped_desc}',
                '${escaped_status}',
                '${escaped_lang}',
                '${now}', '${now}'
            )"
        info "Registered ${C_BOLD}${slug}${C_RESET} \
at ${project_path}"
    fi

    # Check if parent might be a group
    if [[ "${opt_infer}" -eq 0 ]]; then
        _check_group_suggestion "${project_path}"
    fi

    printf "\n"
    info "Config written to \
${C_DIM}${project_path}/.endless/config.json${C_RESET}"
}
