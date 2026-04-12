#!/usr/bin/env bash
# cmd-scan.sh — Scan registered projects for documents and changes

# Directories to skip during document scanning
_SCAN_SKIP_DIRS=".git vendor node_modules .endless/archive"

# Classify a document by its filename/path
# Usage: doc_type=$(_classify_doc "relative/path.md")
_classify_doc() {
    local rel_path="$1"
    local basename
    basename=$(basename "${rel_path}")
    local dir
    dir=$(dirname "${rel_path}")

    case "${basename}" in
        README.md|README.MD|readme.md)
            echo "readme" ;;
        PLAN.md|plan.md)
            echo "plan" ;;
        DESIGN_BRIEF.md|design-brief.md|design_brief.md)
            echo "design_brief" ;;
        CHANGELOG.md|changelog.md|CHANGES.md)
            echo "changelog" ;;
        CLAUDE.md|claude.md)
            echo "claude_md" ;;
        *)
            case "${dir}" in
                design-briefs|design_briefs)
                    echo "design_brief" ;;
                adrs|adr)
                    echo "adr" ;;
                research)
                    echo "research" ;;
                *)
                    # Heuristic: filename patterns
                    local lower
                    lower=$(echo "${basename}" | tr '[:upper:]' '[:lower:]')
                    case "${lower}" in
                        *plan*.md)          echo "plan" ;;
                        *design*.md)        echo "design_brief" ;;
                        *readme*.md)        echo "readme" ;;
                        *changelog*.md)     echo "changelog" ;;
                        *adr*.md)           echo "adr" ;;
                        *research*.md)      echo "research" ;;
                        *)                  echo "other" ;;
                    esac
                    ;;
            esac
            ;;
    esac
}

# Build the find exclusion arguments
_build_find_excludes() {
    local excludes=""
    local dir
    for dir in ${_SCAN_SKIP_DIRS}; do
        if [[ -n "${excludes}" ]]; then
            excludes="${excludes} -o "
        fi
        excludes="${excludes}-path '*/${dir}' -prune"
    done
    echo "${excludes}"
}

# Scan documents for a single project
# Sets _CHANGED_DOCS as a newline-separated list of
# changed relative paths
_scan_documents() {
    local project_id="$1"
    local project_path="$2"
    _CHANGED_DOCS=""

    local now
    now=$(date -u '+%Y-%m-%dT%H:%M:%S')

    # Find all .md files, excluding skip dirs
    local md_files
    md_files=$(find "${project_path}" \
        -path '*/.git' -prune -o \
        -path '*/vendor' -prune -o \
        -path '*/node_modules' -prune -o \
        -path '*/.endless/archive' -prune -o \
        -name '*.md' -type f -print 2>/dev/null)

    if [[ -z "${md_files}" ]]; then
        return 0
    fi

    local doc_count=0
    local change_count=0

    while IFS= read -r filepath; do
        [[ -z "${filepath}" ]] && continue

        local rel_path="${filepath#${project_path}/}"
        local doc_type
        doc_type=$(_classify_doc "${rel_path}")

        # Compute SHA-256 hash
        local content_hash
        content_hash=$(shasum -a 256 "${filepath}" \
            | awk '{print $1}')

        # Get file size (macOS stat)
        local size_bytes
        size_bytes=$(stat -f '%z' "${filepath}")

        # Get mtime as ISO timestamp (macOS stat)
        local mtime_epoch
        mtime_epoch=$(stat -f '%m' "${filepath}")
        local last_modified
        last_modified=$(date -u -r "${mtime_epoch}" \
            '+%Y-%m-%dT%H:%M:%S')

        # Check if document exists in DB
        local escaped_rel
        escaped_rel=$(_sql_escape "${rel_path}")
        local existing_hash
        existing_hash=$(db_scalar \
            "SELECT content_hash FROM documents \
WHERE project_id = ${project_id} \
AND relative_path = '${escaped_rel}'")

        if [[ -z "${existing_hash}" ]]; then
            # New document — insert
            db_exec "INSERT INTO documents \
(project_id, relative_path, doc_type, \
content_hash, size_bytes, last_modified, \
last_scanned) VALUES \
(${project_id}, '${escaped_rel}', \
'${doc_type}', '${content_hash}', \
${size_bytes}, '${last_modified}', '${now}')"
            _CHANGED_DOCS="${_CHANGED_DOCS}${rel_path}
"
            change_count=$((change_count + 1))
        elif [[ "${existing_hash}" != "${content_hash}" ]]; then
            # Changed document — update
            db_exec "UPDATE documents SET \
doc_type = '${doc_type}', \
content_hash = '${content_hash}', \
size_bytes = ${size_bytes}, \
last_modified = '${last_modified}', \
last_scanned = '${now}' \
WHERE project_id = ${project_id} \
AND relative_path = '${escaped_rel}'"
            _CHANGED_DOCS="${_CHANGED_DOCS}${rel_path}
"
            change_count=$((change_count + 1))
        else
            # Unchanged — just update last_scanned
            db_exec "UPDATE documents SET \
last_scanned = '${now}' \
WHERE project_id = ${project_id} \
AND relative_path = '${escaped_rel}'"
        fi

        doc_count=$((doc_count + 1))
    done <<< "${md_files}"

    printf "    %s document(s), %s changed\n" \
        "${doc_count}" "${change_count}"
}

# Check dependency rules from .endless/config.json
# and generate staleness notes for changed docs
_check_dependency_rules() {
    local project_id="$1"
    local project_path="$2"

    local config_file="${project_path}/.endless/config.json"
    if [[ ! -f "${config_file}" ]]; then
        return 0
    fi

    # Read rules count
    local rule_count
    rule_count=$(jq '.documents.rules | length' \
        "${config_file}" 2>/dev/null)
    if [[ -z "${rule_count}" ]] \
        || [[ "${rule_count}" -eq 0 ]]; then
        return 0
    fi

    if [[ -z "${_CHANGED_DOCS}" ]]; then
        return 0
    fi

    local i=0
    while [[ "${i}" -lt "${rule_count}" ]]; do
        local dependent
        dependent=$(jq -r \
            ".documents.rules[${i}].dependent" \
            "${config_file}")
        local depends_on_json
        depends_on_json=$(jq -r \
            ".documents.rules[${i}].depends_on[]" \
            "${config_file}")

        # Check if any depends_on target changed
        while IFS= read -r dep_target; do
            [[ -z "${dep_target}" ]] && continue

            local matched=0
            # Check against changed docs
            while IFS= read -r changed; do
                [[ -z "${changed}" ]] && continue
                # Direct match or glob match
                if [[ "${changed}" == "${dep_target}" ]]; then
                    matched=1
                    break
                fi
                # Glob pattern match (e.g., cmd/**/*.go)
                # Use find to check if the target glob
                # matches any recently modified file
                if [[ "${dep_target}" == *"*"* ]]; then
                    # For glob patterns, check if any file
                    # matching the pattern was modified
                    # since last scan
                    local glob_matches
                    glob_matches=$(find "${project_path}" \
                        -path "${project_path}/${dep_target}" \
                        -newer "${project_path}/${dependent}" \
                        -type f 2>/dev/null | head -1)
                    if [[ -n "${glob_matches}" ]]; then
                        matched=1
                        break
                    fi
                fi
            done <<< "${_CHANGED_DOCS}"

            if [[ "${matched}" -eq 1 ]]; then
                # Check if unresolved note already exists
                local escaped_dep
                escaped_dep=$(_sql_escape "${dependent}")
                local escaped_src
                escaped_src=$(_sql_escape "${dep_target}")
                local existing_note
                existing_note=$(db_scalar \
                    "SELECT id FROM notes \
WHERE project_id = ${project_id} \
AND target_doc = '${escaped_dep}' \
AND source = '${escaped_src}' \
AND resolved = 0")

                if [[ -z "${existing_note}" ]]; then
                    local now
                    now=$(date -u '+%Y-%m-%dT%H:%M:%S')
                    local msg
                    msg="${dependent} may need updating \
because ${dep_target} changed"
                    local escaped_msg
                    escaped_msg=$(_sql_escape "${msg}")
                    db_exec "INSERT INTO notes \
(project_id, note_type, message, source, \
target_doc, created_at) VALUES \
(${project_id}, 'staleness', \
'${escaped_msg}', '${escaped_src}', \
'${escaped_dep}', '${now}')"
                    printf "    ${C_YELLOW}note:${C_RESET} \
%s\n" "${msg}"
                else
                    # Update timestamp on existing note
                    local now
                    now=$(date -u '+%Y-%m-%dT%H:%M:%S')
                    db_exec "UPDATE notes \
SET created_at = '${now}' \
WHERE id = ${existing_note}"
                fi
            fi
        done <<< "${depends_on_json}"

        i=$((i + 1))
    done
}

# Check for document sprawl (multiple files of
# singleton doc types)
_check_sprawl() {
    local project_id="$1"

    # Singleton types: only one file should exist
    local singleton_types="readme plan design_brief changelog claude_md"

    local doc_type
    for doc_type in ${singleton_types}; do
        local count
        count=$(db_scalar \
            "SELECT count(*) FROM documents \
WHERE project_id = ${project_id} \
AND doc_type = '${doc_type}' \
AND is_archived = 0")
        if [[ "${count}" -gt 1 ]]; then
            # Get the file list
            local files
            files=$(db_scalar \
                "SELECT group_concat(relative_path, ', ') \
FROM documents \
WHERE project_id = ${project_id} \
AND doc_type = '${doc_type}' \
AND is_archived = 0")

            # Check if unresolved sprawl note exists
            local existing
            existing=$(db_scalar \
                "SELECT id FROM notes \
WHERE project_id = ${project_id} \
AND note_type = 'sprawl' \
AND source = '${doc_type}' \
AND resolved = 0")
            if [[ -z "${existing}" ]]; then
                local now
                now=$(date -u '+%Y-%m-%dT%H:%M:%S')
                local msg
                msg="Multiple ${doc_type} files \
detected: ${files}. Consider consolidating."
                local escaped_msg
                escaped_msg=$(_sql_escape "${msg}")
                db_exec "INSERT INTO notes \
(project_id, note_type, message, source, \
created_at) VALUES \
(${project_id}, 'sprawl', \
'${escaped_msg}', '${doc_type}', '${now}')"
                printf "    ${C_YELLOW}sprawl:${C_RESET} \
%s\n" "${msg}"
            fi
        fi
    done
}

# Scan a single project
_scan_project() {
    local project_id="$1"
    local slug="$2"
    local project_path="$3"

    if [[ ! -d "${project_path}" ]]; then
        warn "Project path missing: ${project_path} \
(${slug})"
        return 1
    fi

    printf "  ${C_BOLD}%s${C_RESET} %s\n" \
        "${slug}" "${C_DIM}${project_path}${C_RESET}"

    _scan_documents "${project_id}" "${project_path}"
    _check_dependency_rules \
        "${project_id}" "${project_path}"
    _check_sprawl "${project_id}"

    # Update project's updated_at
    local now
    now=$(date -u '+%Y-%m-%dT%H:%M:%S')
    db_exec "UPDATE projects SET updated_at = '${now}' \
WHERE id = ${project_id}"
}

cmd_scan() {
    local opt_project="" opt_docs_only=0

    # Parse arguments
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --project)   opt_project="$2"; shift 2 ;;
            --docs)      opt_docs_only=1; shift ;;
            -h|--help)
                cat <<'EOF'
Usage: endless scan [OPTIONS]

Scan registered projects for document changes.

Options:
  --project SLUG  Scan a single project
  --docs          Scan documents only
  -h, --help      Show this help
EOF
                return 0
                ;;
            *) die "Unknown argument: $1" ;;
        esac
    done

    db_init

    # Log scan start
    local scan_start
    scan_start=$(date -u '+%Y-%m-%dT%H:%M:%S')
    db_exec "INSERT INTO scan_log \
(scan_type, started_at) VALUES \
('$([ "${opt_docs_only}" -eq 1 ] \
&& echo documents || echo full)', \
'${scan_start}')"
    local scan_id
    scan_id=$(db_scalar "SELECT last_insert_rowid()")

    local projects_scanned=0
    local total_changes=0

    info "Scanning projects..."
    printf "\n"

    if [[ -n "${opt_project}" ]]; then
        # Single project scan
        local row
        row=$(sqlite3 -separator '	' "${ENDLESS_DB}" \
            "SELECT id, slug, path FROM projects \
WHERE slug = '$(_sql_escape "${opt_project}")'")
        if [[ -z "${row}" ]]; then
            die "No project found with slug \
'${opt_project}'"
        fi
        local pid pslug ppath
        IFS=$'\t' read -r pid pslug ppath <<< "${row}"
        _scan_project "${pid}" "${pslug}" "${ppath}"
        projects_scanned=1
    else
        # Scan all registered projects
        local rows
        rows=$(sqlite3 -separator '	' "${ENDLESS_DB}" \
            "SELECT id, slug, path FROM projects \
WHERE status != 'archived' ORDER BY slug")
        if [[ -z "${rows}" ]]; then
            info "No projects to scan."
            return 0
        fi
        while IFS=$'\t' read -r pid pslug ppath; do
            [[ -z "${pid}" ]] && continue
            _scan_project "${pid}" "${pslug}" "${ppath}"
            projects_scanned=$((projects_scanned + 1))
        done <<< "${rows}"
    fi

    # Complete scan log
    local scan_end
    scan_end=$(date -u '+%Y-%m-%dT%H:%M:%S')
    db_exec "UPDATE scan_log SET \
completed_at = '${scan_end}', \
projects_scanned = ${projects_scanned} \
WHERE id = ${scan_id}"

    printf "\n"
    info "Scan complete: ${projects_scanned} project(s)"
}
