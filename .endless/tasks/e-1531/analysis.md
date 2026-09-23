Per ED-1509 (epic content-field convention): once this table exists, revisit epic content storage with purpose-specific content types instead of overloading the legacy columns — in particular a 'design' content type for an epic's design/decisions/interface-contracts (currently overloaded into 'analysis'), and possibly distinct 'vision' and 'roadmap' types. Choose types for fit-for-purpose, not by reusing the few existing columns. ED-1509's field semantics are storage-agnostic and survive this migration.


## Scope split settled 2026-09-13: ship the TABLE, defer the POLICY

The reason this has been deferred is the design space it opens: once content is
not constrained to a fixed set of columns, do content names change per task
type, and are names added or removed for specific types? That question is real
and it is large.

It is also SEPARABLE, and separating it is what makes this task shippable.

- **The table is a storage change.** Ship `task_content` carrying exactly today's
  four names with today's semantics, and behaviour is unchanged.
- **The vocabulary is a policy change.** Whether a brainstorm gets `findings`
  instead of `plan` needs no answer for the table to land.

In a row store, adding a name later is an INSERT, not a migration. That is the
entire point of the change — and it means DEFERRING this task is the expensive
option, because until it lands every new content need costs a column.

Corollary Mike raised and worth holding: this will never be one-and-done. New
content kinds get identified on an ongoing basis. So what is settled once is the
CONVENTION, not the list — one lowercase single-token name per content kind, and
the same token used by `task_content.name`, the Agent Folio Format `Name` header,
and the CLI flag. A new content kind then inherits the rule without a fresh
decision, and no mapping between vocabularies is ever needed.

## NOT blocked by E-894

An earlier framing in discussion claimed this needed a working ledger rebuild,
and therefore the Go port first. That was wrong and is withdrawn.

What this actually needs is the PROJECTOR taught to write `task_content` rows,
plus an ordinary SQLite migration moving existing column values into rows.
Rebuild is the disaster-recovery path, not the deploy path. The only true
residual is that rebuild — already broken, and on hold until after E-894 — gains
one more thing it must learn whenever it is fixed. That is not a prerequisite.

## Relationship to the agent-output work

This makes `task meta` cheaper rather than harder. `meta` reports which content
fields exist and how large they are; over columns that is computed per field,
over `task_content` it is one query returning exactly that shape.

It is also where decision content lands if E-1868 proceeds — see E-1861's
analysis for the two-homes split (`tasks.type='decision'` for architectural
decisions, a `decision` content row for implementation decisions) and for why a
decision needs parts rather than one capped description field.



## A first import for this table (noted by E-2155, 2026-09-18)

E-2155's refusal inventory is 1136 rows — one per user-facing refusal or warning
site, with its class, remedy or decision, and notes. It is committed as a TSV
under docs/ because E-2159 reads it row by row while converting, and because
nothing in Endless stores tabular research today.

That file is the exception this table removes. When task_content ships, import
those rows as typed content on E-2155 and delete the file: it is per-task,
typed, and machine-read, which is exactly the shape this table is for. Worth
checking against the schema while designing it — a 1136-row set is a useful
stress case for whether content rows are one-per-artifact or one-per-record.




## Surface words are a per-type display concern, not a second stored name

Folded down from E-1992 when that task became an epic, because the convention
this task already settled is what governs it.

The primary instruction content stays ONE stored name. The word varies only at
the surface, where agents read and write it: `--plan` on todo/bugfix/epic,
`--brief` on research, `--topic` on brainstorm, rendering under the matching
heading. `--plan` stays accepted on every task type as a universal alias, so an
agent reaching for it on a brainstorm lands in the right row instead of an
error.

Storage stays singular because the objection being answered is behavioural — an
agent resists "plan" on a brainstorm — and behaviour is driven by the flag and
the label, not by the schema. Singular storage also keeps the plan-present gate
one question with no per-type mapping behind it.

This sits with the deferred vocabulary policy above, not with the table: the
table ships carrying today's names, and nothing here has to be answered first.
