#!/usr/bin/env bash
# cmd-list.sh — List registered Endless projects

cmd_list() {
    local opt_status="" opt_group=0

    # Parse arguments
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --status)  opt_status="$2"; shift 2 ;;
            --group)   opt_group=1; shift ;;
            -h|--help)
                cat <<'EOF'
Usage: endless list [OPTIONS]

List registered projects.

Options:
  --status STT  Filter by status (active, paused, archived, idea)
  --group       Group by group name
  -h, --help    Show this help
EOF
                return 0
                ;;
            *) die "Unknown argument: $1" ;;
        esac
    done

    # Build query
    local where=""
    if [[ -n "${opt_status}" ]]; then
        where="WHERE p.status = \
'$(_sql_escape "${opt_status}")'"
    fi

    local order="ORDER BY p.group_name NULLS FIRST, p.slug"

    local query
    query="SELECT p.id, p.slug, \
COALESCE(NULLIF(p.name,''),'_'), p.status, \
COALESCE(NULLIF(p.language,''),'_'), \
COALESCE(p.group_name,'_'), p.path, \
(SELECT count(*) FROM notes n \
WHERE n.project_id = p.id AND n.resolved = 0) \
FROM projects p ${where} ${order}"

    # Fetch results as tab-separated
    local results
    results=$(sqlite3 -separator '	' \
        "${ENDLESS_DB}" "${query}")

    if [[ -z "${results}" ]]; then
        if [[ -n "${opt_status}" ]]; then
            info "No projects with status \
'${opt_status}'"
        else
            info "No projects registered yet. \
Run ${C_BOLD}endless register${C_RESET} to add one."
        fi
        return 0
    fi

    # Print header
    printf "${C_BOLD}"
    printf "%-20s  %-20s  %-10s  %-10s  %-10s  %s" \
        "SLUG" "NAME" "STATUS" "LANGUAGE" "NOTES" "PATH"
    printf "${C_RESET}\n"
    printf "%s\n" \
        "--------------------  --------------------  ----------  ----------  ----------  --------------------"

    local current_group=""
    local total=0
    while IFS=$'\t' read -r id slug name pstatus lang group path notes; do
        [[ -z "${id}" ]] && continue
        total=$((total + 1))
        # Normalize NULL placeholders
        [[ "${name}" == "_" ]] && name=""
        [[ "${lang}" == "_" ]] && lang=""
        [[ "${group}" == "_" ]] && group=""

        # Group header
        if [[ "${opt_group}" -eq 1 \
            && "${group}" != "${current_group}" ]]; then
            if [[ -n "${group}" ]]; then
                printf "\n${C_BOLD}${C_CYAN}[%s]\
${C_RESET}\n" "${group}"
            elif [[ -n "${current_group}" ]]; then
                printf "\n${C_BOLD}${C_DIM}[ungrouped]\
${C_RESET}\n"
            fi
            current_group="${group}"
        fi

        # Shorten path: replace $HOME with ~
        local short_path="${path/#${HOME}/\~}"

        # Format status with color (pad first)
        local status_padded
        status_padded=$(printf "%-10s" "${pstatus}")
        case "${pstatus}" in
            active)   status_padded="\
${C_GREEN}${status_padded}${C_RESET}" ;;
            paused)   status_padded="\
${C_YELLOW}${status_padded}${C_RESET}" ;;
            archived) status_padded="\
${C_DIM}${status_padded}${C_RESET}" ;;
            idea)     status_padded="\
${C_BLUE}${status_padded}${C_RESET}" ;;
        esac

        # Format notes (pad first)
        local notes_text
        if [[ "${notes}" -gt 0 ]]; then
            notes_text="${notes} pending"
        else
            notes_text="-"
        fi
        local notes_padded
        notes_padded=$(printf "%-10s" "${notes_text}")
        if [[ "${notes}" -gt 0 ]]; then
            notes_padded="\
${C_YELLOW}${notes_padded}${C_RESET}"
        else
            notes_padded="\
${C_DIM}${notes_padded}${C_RESET}"
        fi

        # Print row
        printf "%-20s  %-20s  %b  %-10s  %b  %b\n" \
            "${slug}" \
            "${name:--}" \
            "${status_padded}" \
            "${lang:--}" \
            "${notes_padded}" \
            "${C_DIM}${short_path}${C_RESET}"
    done <<< "${results}"

    # Summary line
    printf "\n${C_DIM}%s project(s)${C_RESET}\n" "${total}"
}
