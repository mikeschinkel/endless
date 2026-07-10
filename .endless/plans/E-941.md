# Analysis: Universal Filtering for List Commands

## Context

Currently each list command has its own set of `--status`, `--phase`, `--tier`, `--parent` flags with ad-hoc WHERE clause building. There's no way to filter for empty/null fields (e.g. "tasks with no description") or negative matches (e.g. "not auto tier"). We need a filtering approach that handles these cases and scales as fields grow.

## Approach A: Individual Flags (`--no-text`, `--no-description`, `--has-text`)

Add per-field boolean flags for null/empty checks.

**Pros:**
- Discoverable via `--help`
- Simple to implement per-command
- Type-safe, validated by Click
- Familiar CLI pattern

**Cons:**
- Flag explosion: each nullable field needs `--no-X` and `--has-X` (2 flags per field)
- No negative matching for values (can't say "not auto")
- No OR logic between filter groups
- Every new field requires adding flags to every command
- Already have 8+ flags on `task list`; adding more makes `--help` overwhelming

## Approach B: `--empty <field>` / `--nonempty <field>`

A pair of generic flags that accept field names.

**Pros:**
- Two flags cover all nullable fields
- Moderately discoverable
- Can be repeated: `--empty text --empty description`

**Cons:**
- Still no negative value matching (`--status` can include but not exclude)
- No OR logic
- Need to maintain a list of valid field names and map them to SQL columns
- Doesn't reduce the existing flag count

## Approach C: Universal `--filter` with Implicit Field Detection (Mike's Proposal)

Syntax: `--filter needs_plan,quick,no-description --filter ready,-auto`

Rules:
- Values within a `--filter` are ANDed
- Multiple `--filter` flags are ORed
- Known unique values (status/phase/tier) auto-detect their field
- `no-<field>` means `IFNULL(<field>,'') = ''`
- `-<value>` prefix means `<> value` (negative match)

**Pros:**
- Compact: complex queries in a single line
- AND + OR logic without nested parens
- Scales without flag explosion; new fields just need new recognized values
- `no-` prefix handles the empty/null case generically
- `-` prefix handles negative matching for any recognized value
- Feels natural once learned: "needs_plan and quick" reads left-to-right

**Cons:**
- Less discoverable than named flags (need docs or `--help` examples)
- Ambiguity risk: if two fields share a value name, auto-detection breaks. Currently safe: status/phase/tier values are all unique, but must stay that way.
- `no-` prefix could collide if a field name starts with a recognized value (unlikely but worth noting)
- Error messages for typos need care ("foobar" is not a known value -- of what field?)
- Harder to validate at parse time vs named flags

**Collision analysis for current values:**
- Status: needs_plan, ready, in_progress, verify, confirmed, assumed, blocked, revisit, declined, obsolete
- Phase: now, next, later
- Tier: auto, quick, deep, discuss, none
- All unique across fields. Safe today.

## Approach D: Key-Value Filter (`--filter status=needs_plan,tier!=auto`)

Explicit `field=value` and `field!=value` syntax.

**Pros:**
- Unambiguous: no auto-detection needed
- Familiar from tools like `kubectl get pods --field-selector status.phase=Running`
- Easy to extend with operators (`=`, `!=`, `empty`, `notempty`)
- No collision risk

**Cons:**
- Verbose: `--filter status=needs_plan,tier=quick` vs just `needs_plan,quick`
- Users must know field names
- AND/OR still needs a convention (multiple `--filter` for OR?)

## Approach E: Hybrid -- Keep Existing Flags + Add `--filter` for Advanced Use

Keep `--status`, `--phase`, `--tier`, `--parent` as they are. Add `--filter` (Approach C) for:
- Negative matches (`-auto`)
- Empty/null checks (`no-description`, `no-text`)
- OR logic (multiple `--filter` flags)

Named flags and `--filter` would be ANDed together.

**Pros:**
- Backwards compatible: existing usage unchanged
- Simple cases stay simple (`--status ready`)
- Advanced cases are possible (`--filter no-text,quick --filter no-description,deep`)
- Gradual migration: can deprecate individual flags later if `--filter` proves sufficient

**Cons:**
- Two ways to do the same thing (e.g. `--status ready` vs `--filter ready`)
- Interaction rules need documentation (named flags AND `--filter` results)

## Recommendation

**Approach C (implicit field detection)** is the strongest design for where Endless is heading:

1. It's the most expressive with the least syntax
2. The value uniqueness constraint is easy to maintain (and can be enforced with a startup check)
3. AND/OR via comma vs multiple flags is intuitive
4. `no-` and `-` prefixes handle the two missing capabilities cleanly
5. It replaces rather than supplements the existing flags, keeping the interface clean

Implementation path:
1. Build a `parse_filter()` function that maps values to (field, op, value) tuples
2. Build a `filters_to_sql()` function that generates WHERE clause + params
3. Add `--filter` to all list commands, wired through the shared function
4. Keep existing flags working for now; mark as deprecated in help text
5. Enforce value uniqueness across status/phase/tier at module load time

For fields that aren't enumerated values (like `description`, `text`, `prompt`, `parent`):
- `no-<field>`: IFNULL(field, '') = ''
- `has-<field>`: field IS NOT NULL AND field != ''
- `parent=E-799`: explicit key=value for non-enum fields

This gives a clean split: bare values for enums, `no-`/`has-` for null checks, `key=value` for specific field matches.

## Rejected: `|` for OR Within a Single `--filter`

Considered adding `|` as an OR operator within a filter (e.g. `--filter needs_plan|ready,deep`). Rejected because:
1. `|` is the shell pipe character; forgetting quotes silently breaks commands
2. Precedence with `,` (AND) is ambiguous and will confuse users
3. Multiple `--filter` flags already handle OR between groups
4. Same-field OR happens naturally: `--filter needs_plan,ready` where both are statuses collapses to `status IN (needs_plan, ready)` since multiple values for the same field become an IN clause

## Same-Field Collapsing Rule

When multiple values within a single `--filter` resolve to the same field:
- Positive matches collapse to IN: `needs_plan,ready` -> `status IN ('needs_plan','ready')`
- Negative matches each become AND: `-needs_plan,-ready` -> `status != 'needs_plan' AND status != 'ready'`
- Mixed positive+negative on same field is an error

## Verification

1. `endless task list --filter needs_plan,quick` -- AND within filter
2. `endless task list --filter needs_plan --filter ready` -- OR across filters
3. `endless task list --filter no-description` -- null/empty field
4. `endless task list --filter -auto` -- negative match
5. `endless task list --filter has-text,now` -- non-empty field + phase
6. `endless task list --status ready --filter no-text` -- mixed with existing flags
7. Error on unknown value: `endless task list --filter foobar`
