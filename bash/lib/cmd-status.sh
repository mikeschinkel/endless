#!/usr/bin/env bash
# cmd-status.sh — Show detailed status of a project

cmd_status() {
    local slug=""

    # Parse arguments
    while [[ $# -gt 0 ]]; do
        case "$1" in
            -h|--help)
                cat <<'EOF'
Usage: endless status [<slug>]

Show detailed status of a project.

Arguments:
  <slug>    Project slug (default: detect from current directory)
EOF
                return 0
                ;;
            -*)  die "Unknown option: $1" ;;
            *)   slug="$1"; shift ;;
        esac
    done

    # If no slug given, try to detect from current directory
    if [[ -z "${slug}" ]]; then
        local config_file="${PWD}/.endless/config.json"
        if [[ -f "${config_file}" ]]; then
            slug=$(jq -r '.slug // empty' "${config_file}")
        fi
        if [[ -z "${slug}" ]]; then
            # Try matching PWD against registered paths
            slug=$(db_scalar \
                "SELECT slug FROM projects \
WHERE path = '$(_sql_escape "${PWD}")'")
        fi
        if [[ -z "${slug}" ]]; then
            die "Not in a registered project directory. \
Specify a slug: endless status <slug>"
        fi
    fi

    # Look up the project
    local row
    row=$(sqlite3 -separator '	' "${ENDLESS_DB}" \
        "SELECT id, slug, \
COALESCE(NULLIF(name,''),'_'), \
COALESCE(NULLIF(description,''),'_'), \
status, \
COALESCE(NULLIF(language,''),'_'), \
COALESCE(group_name,'_'), \
path, created_at, updated_at \
FROM projects WHERE slug = '$(_sql_escape "${slug}")'")

    if [[ -z "${row}" ]]; then
        die "No project found with slug '${slug}'"
    fi

    local id pslug name desc pstatus lang group path created updated
    IFS=$'\t' read -r id pslug name desc pstatus lang \
        group path created updated <<< "${row}"

    # Normalize NULL placeholders
    [[ "${name}" == "_" ]] && name=""
    [[ "${desc}" == "_" ]] && desc=""
    [[ "${lang}" == "_" ]] && lang=""
    [[ "${group}" == "_" ]] && group=""

    # Format status with color
    local status_colored
    case "${pstatus}" in
        active)   status_colored="${C_GREEN}${pstatus}${C_RESET}" ;;
        paused)   status_colored="${C_YELLOW}${pstatus}${C_RESET}" ;;
        archived) status_colored="${C_DIM}${pstatus}${C_RESET}" ;;
        idea)     status_colored="${C_BLUE}${pstatus}${C_RESET}" ;;
        *)        status_colored="${pstatus}" ;;
    esac

    # Shorten path
    local short_path="${path/#${HOME}/\~}"

    # Print project header
    printf "\n${C_BOLD}%s${C_RESET}" "${pslug}"
    if [[ -n "${name}" ]]; then
        printf "  ${C_DIM}(%s)${C_RESET}" "${name}"
    fi
    printf "\n"

    # Description
    if [[ -n "${desc}" ]]; then
        printf "${C_DIM}%s${C_RESET}\n" "${desc}"
    fi
    printf "\n"

    # Details
    printf "  %-14s %b\n" "Status:" "${status_colored}"
    printf "  %-14s %s\n" "Language:" "${lang:--}"
    if [[ -n "${group}" ]]; then
        printf "  %-14s %s\n" "Group:" "${group}"
    fi
    printf "  %-14s %s\n" "Path:" "${short_path}"
    printf "  %-14s %s\n" "Registered:" "${created}"
    printf "  %-14s %s\n" "Updated:" "${updated}"

    # Document count
    local doc_count
    doc_count=$(db_scalar \
        "SELECT count(*) FROM documents \
WHERE project_id = ${id} AND is_archived = 0")
    printf "  %-14s %s tracked\n" "Documents:" "${doc_count}"

    # Pending notes count
    local notes_count
    notes_count=$(db_scalar \
        "SELECT count(*) FROM notes \
WHERE project_id = ${id} AND resolved = 0")
    if [[ "${notes_count}" -gt 0 ]]; then
        printf "  %-14s ${C_YELLOW}%s pending${C_RESET}\n" \
            "Notes:" "${notes_count}"
    else
        printf "  %-14s ${C_DIM}none${C_RESET}\n" "Notes:"
    fi

    # Dependencies
    local deps
    deps=$(sqlite3 -separator '	' "${ENDLESS_DB}" \
        "SELECT p2.slug, pd.dep_type \
FROM project_deps pd \
JOIN projects p2 ON pd.depends_on_id = p2.id \
WHERE pd.project_id = ${id}")
    if [[ -n "${deps}" ]]; then
        printf "\n  ${C_BOLD}Dependencies:${C_RESET}\n"
        while IFS=$'\t' read -r dep_slug dep_type; do
            printf "    %s ${C_DIM}(%s)${C_RESET}\n" \
                "${dep_slug}" "${dep_type}"
        done <<< "${deps}"
    fi

    # Dependents (who depends on this project)
    local dependents
    dependents=$(sqlite3 -separator '	' "${ENDLESS_DB}" \
        "SELECT p2.slug, pd.dep_type \
FROM project_deps pd \
JOIN projects p2 ON pd.project_id = p2.id \
WHERE pd.depends_on_id = ${id}")
    if [[ -n "${dependents}" ]]; then
        printf "\n  ${C_BOLD}Depended on by:${C_RESET}\n"
        while IFS=$'\t' read -r dep_slug dep_type; do
            printf "    %s ${C_DIM}(%s)${C_RESET}\n" \
                "${dep_slug}" "${dep_type}"
        done <<< "${dependents}"
    fi

    printf "\n"
}
