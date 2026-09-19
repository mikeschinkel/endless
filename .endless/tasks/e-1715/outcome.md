# E-1715 findings — Backfill historical task landings from git history

## Verdict

Reliable landing-recording began **2026-05-21** (first `task_landings` row), the day after
**E-1337** ("record landings") landed on main (**2026-05-20 22:04**). Every task that landed
before then has no landing row *by construction* — that is the backfill population, not loss.
Matching main's commit subjects to task ids yields **177 confident backfill candidates** (down
from 178 after excluding the reverted E-1108), **25 that need manual review**, and a small set
of decisions/orphans that are N/A. A turnkey, dry-run-by-default emit script is embedded below.

**Correction to the ticket:** the landing recording that actually *shipped* was **E-1337**
(commit `832b7aab`, landed 2026-05-20) — task_landings + task.landed emit. E-1392 (the ticket's
attribution) does exist, but as a still-`ready`, unimplemented task ("Add task.landed event plus
session_landings table plus 'endless session landings' CLI"); E-1337 delivered an early form of
it. So recording is E-1337's; E-1392 remains open for the broader session_landings/CLI piece.

**Sanctioned backfill mechanism is E-1479, not the raw script below.** **E-1479** (`ready`,
blocked_by E-1486/E-1537) adds `endless worktree land <id> --record-only --sha … --at …`, which
emits a proper `task.landed` for an already-completed land: it makes `task_landings.branch`
**nullable** (records NULL when the original branch is unrecoverable — the correct value, vs.
the synthesized `task/<id>-slug` names the script below fabricates) and sets `landed_at` to the
real **commit date** via `--at` (the script uses now()). E-1479 explicitly scopes *out* the
broad historical backfill, so the script/list below is the **input dataset** for a follow-up
that drives `--record-only` over these 177 (id, sha) pairs once E-1479 lands. The embedded
`endless-go event emit` script is a lower-fidelity fallback (fake branches, now() timestamps) —
prefer E-1479.

**Ground truth is the DB, not git.** The task→commit link lives only in `task_landings`
(`task_id, session_id, branch, merge_commit_sha, landed_at`). `worktree land` is rebase +
`git merge --ff-only` (no merge commit) + a `task.landed` event — so there is no landing commit
in git. Commit-subject matching is a *heuristic reconstruction* of what the DB would have
recorded; it is high-confidence for the tiers below but is not the same as an authoritative record.

## Method (read-only; `--db main` + git on the main checkout)

1. `git log main` → 1091 commit subjects. Extract task-id references and classify by convention:
   - **Confident** = the commit *carried the landed work*: `E-N:` / `Verb E-N:` colon-header,
     `(E-N)` suffix, or merge-era `Merge branch 'task/N-…'`.
   - **Review** = plan-edit commits (`Add/Update E-N plan`, `refine plan`, `Endless: … plan for
     E-N`) or a bare body mention only — the plan/mention landed, the code landing is unconfirmed.
2. `SELECT DISTINCT task_id FROM task_landings` → 126 ids already recorded (164 rows total).
3. Subtract recorded ids; join against the `tasks` table (951 endless tasks).
4. `merge_commit_sha` = the confident commit's full sha. `branch` recovered from surviving
   `task/*` refs / HEAD reflog where present (only 9 — the old cohort's branches are long gone),
   else synthesized `task/<id>-<title-slug>` and marked `(synth)`.

## Reconciliation (334 distinct ids referenced across main commits)

| Bucket | Count | Disposition |
|---|---:|---|
| Already have a landing row | 123 | none needed |
| **Confident — feature commit** | 157 | backfill (156 after E-1108 excl.) |
| **Confident — merge-era merge commit** | 21 | backfill |
| Review — plan-edit commit only | 14 | hand-check whether code landed |
| Review — bare mention only | 11 | hand-check |
| No task row: 7 decisions | 7 | N/A (decisions don't land via task_landings) |
| No task row: E-783 orphan | 1 | task deleted; feature landed — nothing to attach to |

Spot-checks: E-1209 `6671bca9` is an ancestor of main ✓ (the motivating case); E-1669
`6603c394` ancestor ✓; E-1108 land→revert pair ✓; earliest landing row 2026-05-21 vs E-1337
land 2026-05-20 ✓.

## Edge cases to know before emitting

- **E-1108 — EXCLUDED.** Landed (`fb60208d`) then **reverted** (`b78ce4ad`); net not on main.
  The script drops it.
- **E-1669 — the one genuine gap.** Obsolete, created 2026-06-27, feature commit `6603c394` is
  an ancestor of main and not reverted, yet has no row: a real post-2026-05-21 recording miss
  (same class as E-1223/1224 from E-1710). It is included; drop it if you'd rather not record an
  obsolete task's landing.
- **Plan/design commits that use the colon-header form** slipped into the confident tier:
  **E-987** (research; "resolve addendum design questions"), **E-1064** (research; "Correct
  E-1064 audit"), **E-968** ("Refine E-968 plans"). Each is arguably a plan/outcome commit, not
  a code landing — review individually.
- A few obsolete tasks are in the confident set (E-750, E-1275, E-1354, E-1409). Their commits
  are on main; whether an obsolete task "landed" is a judgment call. **E-1409** shares E-1401's
  commit `d34b2602` (two tasks, one commit).
- **Multi-task commits**: `6b6e9bbe` carried E-448/687/712/750 together — each gets its own row
  pointing at that shared sha (correct: they landed together).

## Manual-review list (25 — not in the script)

Plan-edit-only (14): E-894, E-971, E-993, E-995, E-1031, E-1084, E-1085, E-1113, E-1129,
E-1182, E-1186, E-1272, E-1275, E-1333.
Bare-mention-only (11): E-917, E-918, E-921, E-991, E-992, E-1002, E-1037, E-1126, E-1137,
E-1335, E-1509.
No task row: decisions D-958, D-1007, D-1094, D-1175, D-1199, D-1361, D-1373; orphan E-783
(feature commit `Add --status flag to task add command (E-783)` on main, task row gone).

## Recommended execution (a shared-ledger mutation — run on the MAIN checkout)

The script below emits one `task.landed` per candidate via `endless-go event emit`
(`--actor-kind system --actor-id backfill`, exempt from the attribution gate; dedicated
`--node-id ba15` isolates backfill segments — mirroring the existing synthetic `abcd` segment
precedent). `emit` guarantees a valid Kairos `ts`, envelope validation, the ledger git-commit,
and the `execTaskLanded` INSERT — safer than hand-authoring JSONL.

Notes / decisions for you:
- **Commit volume**: 177 emits → 177 `Endless: record ledger entry` appends/commits on main
  (amended where the tip is unshared per `canAmend`). Acceptable, or squash afterward. The
  alternative — one hand-authored segment + a single commit (the `abcd` pattern) — is cleaner
  but bypasses `emit`'s validation; not recommended.
- **`merge_commit_sha` semantics**: for the 156 feature rows this is the commit that *carried*
  the work, not a distinct ff-merge tip (none existed in the rebase era); for the 21 merge-era
  rows it is the real merge commit. `landed_at` on replay derives from the event `ts` (= now,
  when you run it), not the original land time — acceptable for a backfill, or hand-author `ts`
  if historical ordering matters.
- Run **dry-run first** (no args), eyeball, then `--apply`. Verify with
  `endless-go event validate-db` or `endless task landed <id>`.

```bash
#!/usr/bin/env bash
# E-1715 backfill: emit task.landed events for historical landings that predate
# reliable landing-recording (first real task_landings row = 2026-05-21; E-1337
# landed 2026-05-20). Reconstructs task->commit links from main's git history.
#
# GROUND RULES
#  - Run from the MAIN checkout only (~/Projects/endless). endless-go's auto-commit
#    refuses to run in a linked worktree; each emit also makes a commit on main.
#  - Shared-ledger mutation. Review the list, then run with --apply.
#  - Default is DRY-RUN (prints commands, emits nothing).
#  - 'system/backfill' actor is exempt from the session-attribution gate.
#  - Dedicated node id 'ba15' isolates backfill segments from real nodes.
#  - E-1108 is EXCLUDED (landed then reverted; not on main).
#  - E-1669 IS included but is the one genuine post-2026-05-21 recording gap;
#    it is 'obsolete' now — drop it from the list if you'd rather not record it.
#  - branch values marked (synth) are reconstructed from the task title, not the
#    original branch (which no longer exists as a ref or in the reflog).
set -euo pipefail

MAIN="${ENDLESS_MAIN:-$HOME/Projects/endless}"
GO="$MAIN/bin/endless-go"
NODE=ba15
APPLY=0; [[ "${1:-}" == "--apply" ]] && APPLY=1

cd "$MAIN"
if git rev-parse --git-dir 2>/dev/null | grep -q worktrees; then
  echo "REFUSED: run from the main checkout, not a worktree." >&2; exit 1
fi
[[ -x "$GO" ]] || { echo "missing $GO (run: just build)" >&2; exit 1; }

# task_id | merge_commit_sha | branch   (branches without a surviving ref/reflog are synthesized)
read -r -d '' DATA <<'EOF' || true
448	6b6e9bbe896979e818fd21f349048198447bdbd9	task/448-read-tmux-endless-task-id-to
572	99537dcccfb873f07de69c666da2bec5e8501edb	task/572-remove-task-dependencies-rename-no-longer
687	6b6e9bbe896979e818fd21f349048198447bdbd9	task/687-show-child-status-indicators-on-collapsed
696	a04b061921d3326a14b67f07a75817c3727c7b67	task/696-cascade-or-warn-on-task-remove
712	6b6e9bbe896979e818fd21f349048198447bdbd9	task/712-test-channel-close-end-to-end
713	c24d2371162183b0dfcb9317ade6362e7f5cede6	task/713-update-session-guide-with-inter-session
730	1675c55d9eec76b09f6adb97e9cfadb598010c3d	task/730-add-task-search-command
750	6b6e9bbe896979e818fd21f349048198447bdbd9	task/750-add-status-change-dropdown-on-status
755	576e8910e5cdc9894718ff8110ea1bf198b319ea	task/755-fix-task-tree-toolbar-scrolling-off
787	c32f0680397e57bc369e486fa75c5cb917671683	task/787-add-declined-status-and-outcome-field
803	4e565b754727f8ed6aba912824428d5625138b6a	task/803-implement-hybrid-logical-clock-serialization-package
804	f99b9ab01067ba78f070493c1eb5aec08c7e02ce	task/804-define-event-envelope-and-closed-event
805	75d7b7a39931cedf3bb534f52176a7a6d120e646	task/805-implement-go-event-writing-cli-subcommands
807	812e13889cff7355fa267536b5864bf77e9369d8	task/807-build-projection-engine-to-rebuild-sqlite
808	c9425fb7fe6c7ce5e4b02737e606fccf083f9798	task/808-refactor-write-path-to-make-event
854	c688438772cf32e21ad973da1ab13169d821838b	task/854-add-obsolete-status-for-tasks-overtaken
855	5fc0456f01142f11d0ca438e714e500b2625070c	task/855-add-check-constraint-and-auto-ready
856	6ab4c49219859cc7ffecb1450410e18cf4d08c12	task/856-clear-tier-to-n-a-on
857	6c2dd3dcee25f71d2e8459fc73c71ac026ebb9a9	task/857-build-session-conversation-history-feature
860	b693cc0bd8c18e9888841cba4eca6b9759b8ce33	task/860-generate-session-recaps-in-background-refresh
861	b2d44ee887041b14f9b672a5bbb79dc766a8806e	task/861-add-text-flag-endless-task-add-body-file
862	59e8536edec75f56611725861c892d22dbd539af	task/862-split-migrations-into-auto-and-manual
863	66ccaf34a4440414a87cb84dbe3928579055535a	task/863-implement-schema-version-migration-system
864	808358bdf39a1ef0b23a9d46a0555af8c0391e1b	task/864-add-limit-option-to-task-search
865	069f520e50b30635cb73fa44a6853c5885434715	task/865-add-pragma-busy-timeout-to-all
866	1cc95a2d156056e706758823d7fe685d92550e0b	task/866-add-shell-prompt-hook-for-async
870	59b8d3c3bc80171fcf15c9f48c205584c7fffd33	task/870-migrate-session-recap-generation-from-python
871	a82ca678ded4ca0a15ea9095370b8e9bbd62726f	task/871-fix-hook-for-unregistered-project-directories
872	d0a9315004eb1a1de66230e7b52a78291c7f4fd9	task/872-add-phase-filter-to-task-next
874	f43234dd0f3520af4af9124997a74c50da75d862	task/874-fix-go-mod-replace-to-use
875	257e082dcc1afe7d0ddf7bf5d1a12c754d5e75af	task/875-fix-dependency-display-show-resolved-vs
876	26299c978b8e1039ec5ee827866489a4f94227db	task/876-document-blocking-semantics-verify-blocks-assumed
877	d2d4a9c819d49d37fbc4baafdec241df7dfbf054	task/877-add-timestamp-to-server-restart-message
878	8976b768425fe0997429c8d73bc3033347080934	task/878-add-emergency-db-backup-on-every
879	d29e84333d724c19a91c5ab0848200878e456223	task/879-fix-status-dropdown-no-wrap-and
880	6cf259d6648b7c8b9dfffc09972e632fab20ae1d	task/880-move-status-dropdown-from-sphere-double
881	b7c2ce1ceb486ac557543b8bf0b636c38421baf5	task/881-add-status-descriptions-to-detail-pane
890	68b7986c3d031c4848e69c15d0a394f9d5898957	task/890-add-replaces-replaced-by-relationship-type
892	21ab6be5dff732e5091df314406e1b09030672eb	task/892-remove-all-check-constraints-from-sqlite
909	df8c12500d9b5ef9fbcff33cabec0ce72986a3b6	task/909-add-llm-flag-to-task-deps
922	a5b8c6a3dd55dbbd66efdd8b0dc95516757a7d15	task/922-raise-max-event-line-size-from
949	d4d420782511bfba319f2879d544b1dda7eae5f5	task/949-refactor-endless-config-loading-onto-go
957	645a58bd31312d56ee9bcd417bd1c42fbaae90fa	task/957-add-general-purpose-task-relation-cli
967	9ce47a8a2b7ddd55468f58bd70bd36ee8d720254	e-967-detail-alias
968	2979f1e6f3af4cca56e00e67d8e5a74ba5394045	task/968-add-parallel-session-support-via-git
969	f1e953dfbdd12feb8efb56cbbbbe8435efad90a6	task/969-add-plan-file-copy-on-attach
970	964509a125efcdb3d2034a4d1acd56df3fa1a4d2	task/970-add-phrases-table-for-shared-string
972	351ec450f3ed78ba0422cbb27073ba0b1327e3ab	task/972-fix-endless-project-resolution-to-detect
976	32c4b7521c2ccabeea61e7bf5f487ee1d771b294	task/976-add-endless-worktrees-to-gitignore
980	bb41670ef319110725e8167f8980b25c597b3983	task/980-add-decision-flag-to-task-add
982	8e4bee7074aad4fc8fd784b5c5b61d11f5a4110d	task/982-add-verify-and-reconcile-to-title
983	611bbceae399794f0832acacd58df20d235bce30	task/983-add-prune-to-title-verbs-allowlist
987	be67028726ad5cd6250d3074aa2beeb8f8836a7a	task/987-resolve-tension-between-event-log-auto
988	f6ea405e8eee7b55f4c305c8786f3a31c826405f	task/988-fix-newlines-in-session-list-summary
989	6f43859517474cdab0029ab619364d7b237d2d39	task/989-add-session-to-pane-companion-file
990	d5f94f68ac9a5dd35cd29503b31347d2e0d33f40	task/990-add-endless-session-cd-to-switch
994	8dbb32af762172c79248282fc0ffea909351b095	task/994-reconcile-snapshotplanfile-error-handling-against-fatal
996	b7f12cb97158c50ece6b2fe79265504cdbf1b2f0	task/996-fix-go-mod-replace-paths-to
998	564ce2bc34d652ecbb0d6ebd7646ff621b1422c9	task/998-add-per-worktree-claude-settings-json
1004	a9ba29fc1b837deb5d11ee237f37ec162d608410	task/1004-fix-project-root-for-task-to
1006	f3b2e195fb1866fd401c171dd46edda502ba8faf	task/1006-add-documents-documented-by-dep-type
1008	815d4fb0111e3bc26e489b959c4b54e733f36d71	task/1008-convert-endless-cli-install-to-editable
1011	5e12ac7b19f25c9f5d178bc844b0f6959c171d70	task/1011-backfill-missing-companion-file-on-userpromptsubmit
1012	4f69a0560318c872859945c14e26eceac1f50939	task/1012-add-pretooluse-hook-rule-to-block
1014	7d706179382307c94e3ae1bb1c3ab3f005675837	task/1014-add-endless-session-use-with-pluggable
1015	914030a8533f333f19b89b67ef6c3112e0ff0458	task/1015-add-endless-shell-init-to-print
1020	e18071050e5599a64ff64c509582af3423fe8c8f	task/1020-add-xdg-config-home-support-to
1027	7219eb99f782f95bc831691723789c122e5f3117	task/1027-populate-companion-file-worktree-path-from
1028	6e048ea4fdaf11ec80bdddd02e8f74faf5657c92	task/1028-fix-hook-task-start-complete-regex
1030	155f56aed9faf7559702ff3c18be94fce9e150fe	task/1030-show-project-name-in-task-show
1033	58ac6c8e3e490ab17b97924d5effa999799bf258	task/1033-refresh-companion-file-on-every-userpromptsubmit
1038	9ea22b33b5b44145496d5016425b7b7d7bcda6ca	task/1038-reduce-session-use-activation-to-endless
1047	ee7845700c729b0d9da6c414670e3d5b13d9cb99	task/1047-add-status-messages-to-session-use
1049	2f339bff3668ceb62be8c9905cae735782b9e53e	task/1049-shorten-home-directory-to-in-session
1050	4346993a5a01cd3c72a30cbdf9264194c468f0fe	task/1050-add-target-flag-to-session-cd
1059	4ce90a9e95e4bd07a8b0f74d484aed649c59eb67	task/1059-validate-description-on-write-cap-at
1064	ebf17d844ac4ef31c6a56b68f4a3c30cb753df35	task/1064-audit-analysis-fields-on-tasks-for
1074	bc964ede0d91d6a8afab5e3a11f1d9ae98fe1b1d	task/1074-implement-endless-sandbox-go-binary-e
1088	4f5bfa1e9ebfaf6803d156d5eaf73dbc6967a51b	task/1088-add-maybe-phase-to-task-phases
1101	071787a191cd61bc1fa06a5615f4216975ff7af6	task/1101-validate-phase-value-at-events-projector
1106	32d91504772d908e989d7450da5338bfdb5dc77e	task/1106-redesign-verb-gate-error-to-suppress
1111	74176b2796ebacc3e2a7645b46014f24029f3b89	task/1111-fix-project-config-writes-from-worktrees
1114	6d95344eb8ad96982c60b5c8e81fc33e5bf0fbf6	task/1114-isolate-endless-sandbox-enter-subshell-in
1117	1218b33f064d4da49ce080eceb6a6cb9bacb7953	task/1117-redesign-verb-storage-extract-verbs-from
1118	8dce3a615ad0eeb3f3f1141e5ff27272201ce2db	task/1118-fix-migrate-v5-non-idempotent-swap
1120	d18e8b1658ad35b146d9871a31a751a41f7d31b9	task/1120-show-label-old-new-on-all
1121	b9586f3d37f09dd51a69471b30ffe177865df72b	task/1121-apply-click-choice-to-task-add
1122	5b92efad563b099097e8b3be6c76964574688020	task/1122-migrate-all-machine-layer-verbs-into
1124	7084b5cb4e2708ae5141037f07b0d4fd18e6742d	task/1124-extract-verbs-from-config-json-into
1130	7b28b4a1754c8cb39fcd801804d5382b6866cf19	task/1130-skip-endless-hook-work-when-cwd
1135	ce6f1afb67b9b61fa0d8984c73ab8a82199256fa	task/1135-add-worktree-flag-to-endless-task
1138	09f2d7167a20e19f6f926d7b56d99784a25e9f33	task/1138-update-worktree-land-auto-commit-list
1139	72a2162d1f115f7c918ee15de634c460a6d08e88	task/1139-add-endless-decision-link-for-typed
1144	14d1cb68285b69dd002d4b6b74a41df2db4e8574	task/1144-change-tmux-window-label-created-by
1145	61984e15b6e451b992f7aa4a1f543d0d46542d2d	task/1145-add-cleans-up-cleaned-up-by
1149	1330281cc510e801031af6cf0f2109b4f55c95f1	task/1149-rename-as-to-type-on-task
1154	47782c00218088aad010ab314caa6375ad5e03ed	task/1154-block-endless-sandbox-destroy-when-live
1159	f9b95a9748ede1cda8316a9c671a76c71fb8c8b8	task/1159-add-endless-session-forget-subcommand-and
1160	b6a222f1e7e3b29a5cc18549b925865f3d58aa82	task/1160-replace-sqlite-traceback-with-actionable-error
1161	d2d4497cd09893f358b2f677f73d783810fc01f7	task/1161-update-endless-session-use-docstring-and
1162	0668c5957ffadee5c1057322612ca4d4e7149f1f	task/1162-detect-endless-sandbox-in-endless-cli
1164	e1b2517466a5998c58030a0f6ca6ba324bccee8a	task/1164-route-shell-helpers-to-worktree-cli
1170	441fd28f4ad594eb83850f6c1c2c02cf05a00a74	task/1170-add-auto-creation-of-task-worktree
1172	129ca55e4c695f901622d0f854285b9ebf5c4ca1	task/1172-add-if-exists-flag-to-endless
1187	cf3e9e54ea4f291c05e015ddecc5bb11138eb4f2	task/1187-rename-worktrees-and-code-paths-from
1196	b415cd69e948f0ceff3bdf14e6c1a0407d1a617c	task/1196-catch-deleted-cwd-filenotfounderror-and-exit
1197	28e9d151641c882d49e1c8ef929a451d3503fd80	task/1197-rename-endless-events-endless-db-ledger
1203	20278950ecf209b027b883ef359e4033bf25bddf	task/1203-refuse-endless-task-start-e-nnn-if
1206	3937833650a4bd33c4356dc2699c4cbed8082a69	task/1206-record-each-endless-db-ledger-write
1208	98b4ca6f11c0449ee0b514b2841dc9c6d4bb6a20	task/1208-record-verbs-json-changes-immediately-on
1209	6671bca9fd1d18a94973f95fa48f0a937fd3bbcf	task/1209-fix-endless-worktree-land-failing-to
1210	c1711b8899e045857c97e01b776ef23b7233c956	task/1210-remove-migratelegacyledger-dual-path
1216	58e696c8c948b99a6407265c00365869984706ef	task/1216-move-plan-file-writes-from-main
1218	2318377943d19bccec03eacfd4f922ca0737ce43	task/1218-untrack-endless-worktree-json-and-endless
1219	be4041cdd3ce30cfc6c8892e6132ec359614cd3e	task/1219-audit-findworktreeroot-walk-up-to-handle
1225	81b4564d0296466a0525b089dd59edd828dc58fe	task/1225-add-endless-sql-verb-for-read
1226	bfae79ed057c6c8c48b557fda2972c771148ef45	task/1226-add-a-pretooluse-hook-to-refuse
1229	b4a8c55c984eccf840b3cdb45a643363fbe6605b	task/1229-add-already-active-in-this-session
1232	fad6d24c88f4406161d119a6a744606bccbdd6b6	task/1232-rename-task-start-task-claim-add-task
1234	10bf1eaa6ea52af2981854a58d6c7d8bc557b1d6	task/1234-add-deprecated-but-functional-alias-for
1235	fcfad3f3f8f583c4a3e9cb90655011398041f240	task/1235-refuse-task-claim-on-a-task
1236	a15d186d5c614b566777749bc7b49042cd1adaea	task/1236-add-endless-tmux-integration-ephemeral-apply
1238	ca2ba6982c575abf47d120e2f16712ad8af9c345	task/1238-apply-e-1219-audit-recommendation-findworktreeroot
1240	55c3163f8b5b099c140c568b20136ed8f29e4212	task/1240-add-completed-status-gated-by-verbs
1241	726e286e8a853eab9f515d4d63568883297ef39d	task/1241-refuse-task-start-deprecation-alias-instead
1242	3e43672175af5f0fe8de0eb84180c3e6b5c52aaa	task/1242-bind-sibling-pane-claude-session-on
1243	6e98dc554d4c96ab9e3fcefcea277f1b5798f011	task/1243-fix-task-release-to-handle-missing
1245	120de2c72b489a45d101fdb775b7aa515fe2562f	task/1245-fix-status-row-pane-fallback-to
1246	f53d9b21f8a07277917f79648ed180354e24fac5	task/1246-improve-tmux-status-line-menu-labels
1248	9a0326aac9ca3c6e120d251b6cdc1ce7c462d463	task/1248-research-tmux-capabilities-surfacing
1251	7eb9e81b948f2a4ff9dad9bd623869b6a45f8162	task/1251-fix-right-click-menu-position-regression
1253	4c9909cafacf1fac34db07bd1c72c0d8a86cc8f8	task/1253-fix-right-click-menu-to-anchor
1255	f555fc479b77da01145f342dfa0bfa66b1c72bea	task/1255-fix-right-click-menu-vertical-position
1256	3361595ad6dd05ed0c2cfcbbd9ed91b4882f9edc	task/1256-bind-session-to-a-task-without
1257	bc21d9f371ed3e0e8755517f661131a21968c29c	task/1257-show-contextual-hints-on-endless-status
1258	50a6060ba26e995d5bc48fc140010734acc1167b	task/1258-add-menu-option-to-toggle-endless
1264	05a42838be90610241e8f05c85cf7c0776a4f88f	task/1264-register-verbs-automatically-via-claude-haiku
1266	768e3002eb366b5de974a1d18db1b9156b22c74c	task/1266-promote-needs-plan-to-ready-when
1268	54e346d2a7fe663049ca2303de2fd3b700992fa3	task/1268-convert-verbs-json-to-verbs-jsonl
1269	fc564f64dd2a29012e6a22662e469d733d0475b5	task/1269-strip-internal-task-ids-e-nnn
1270	02b82b5bc64c346f638bb6a2f2fea1044ac3f08b	task/1270-strip-parentheticals-from-user-facing-guide
1274	1137aacdb3483732dfd3d8ee65bc109e830c82f4	task/1274-fix-spawn-pre-binding-session-to
1281	247f999672cb0bef340c2feee1ae7db841d929ac	task/1281-fix-worktree-sandbox-writing-to-global
1284	85ab89febc9b8937e8c71318e1eed2e65cf92a1c	task/1284-add-session-id-to-event-actor
1285	64b3eae2407999ad3cf08ce76c3fc84361ace87f	task/1285-add-session-activity-report-cli-and
1287	fc5eedd3de5b8f2c0cae8e378b101f2ac04d6705	task/1287-extend-emit-event-session-resolution-to
1291	27b1907ac3fc50917c1b062d2b39ea07fbfd816d	task/1291-auto-bind-session-to-task-by
1294	d6124087b9fa5498f7c000a1aee97a11ca85431c	task/1294-extend-current-endless-session-id-with
1298	352af9af0faf1a24b4edc7ca6d0da3a8aa600727	task/1298-detect-stale-binary-after-worktree-land
1300	5f31542a81be832b0b31c613aadeca916f10fc94	task/1300-skip-subagents-in-spawn-flow-sessionstart
1301	b4a52bb2fa19becc85e1b4c516ac725d58187b44	task/1301-migrate-readers-of-endless-worktree-json
1307	8bd939fe8bba282d9c48ea7e3cb6f41d7fc53ab0	task/1307-add-endless-session-id-verb-to
1309	f039088af423af0a88f0cf96b23cd9aa9a41734b	task/1309-route-ledger-auto-commits-to-project
1312	41abeee955ef2731bc141010be8dfc9e846f4cd6	task/1312-add-endless-session-status-add-command
1313	5d6081f313892bfd70238f825fc6aa57a7aaa511	task/1313-fix-endless-sql-write-missing-commit
1314	36443d127d4302c25b880758cee1c5b69e250c21	task/1314-extend-session-statuses-add-active-task
1315	00c949464654031c19c9706881cb4f6af3ec90d2	task/1315-fix-deadlock-in-execsessionstatusrecorded-use-tx
1317	6e368133827d17dbc95d0510958ef6cb8755713d	task/1317-pass-pane-id-to-session-activity
1320	7b999eccd76c25165069e36bce8181875a523730	task/1320-add-docs-guide-sessions-md-and
1322	cceb60f8a3b2d3a3bfa43d8f9567db8ea0dad120	task/1322-add-session-tasks-table-auto-capture
1329	e35ebde546695eb90e03dbf3c33fed0fb78e7e3c	task/1329-add-type-and-analysis-flags-to
1337	832b7aab42d4d24caf0f583b169c854dd6673a1f	task/1337-replace-worktree-delete-on-land-with
1342	151e96922b0772be0f78007c1c31bae7b2bbf113	task/1342-auto-detect-and-drop-orphan-ledger
1345	5c20f540eedb3acf081881f00dab07a56765ad9a	task/1345-apply-schema-sql-as-v0-baseline
1347	3b3168c73bf3209b3f5127ac07141303bcbc0950	task/1347-set-skip-worktree-on-claude-settings
1351	8162208dac9afb9127712fac381fd7d9af4e9eee	task/1351-land-retry-must-also-catch-diverging
1354	ec7778ce45d30cacbaa23bd935bfe47c516eb211	task/1354-resolve-snapshotplanfile-s-projectroot-to-the
1355	0f3fe75a106c58efc31a76cd0264ac8fe0dccd09	task/1355-fix-orphan-drop-in-land-detaching
1381	fe3f8513babe361d14f1db073a668170c7dce11e	task/1381-fix-shouldrecordsessiontouch-guard-drop-actorkind-check
1395	e21242288010ad61725d2dc869f7cab91048c208	task/1395-fix-tmux-window-pane-ids-scope
1401	d34b26026a395c28ff1a3da412d0cc4be1c5088d	task/1401-gate-event-emission-on-resolvable-session
1402	b55cab6b36504ef6a5a6eca546624133b7bb0080	task/1402-strengthen-session-resolver-to-handle-multi
1405	22eb202ffaee55e927b1a1bd1a72511d2de45bc7	task/1405-add-urgent-phase-above-now-for
1409	d34b26026a395c28ff1a3da412d0cc4be1c5088d	task/1409-ship-endless-session-id-subcommand-that
1416	5276be29f83e460dbdc3d36ffd2fb65e8d2c0558	task/1416-guard-against-dirty-unmanaged-files-in
1426	18b872bd23dd2934a3f239a901da8fab835be182	task/1426-replace-session-companion-json-files-with
1669	6603c394eff886726bd6f047a7a42d2b731e03d1	task/1669-provision-self-dev-worktrees-so-hook
EOF

echo "Backfill candidates: 177 (E-1108 excluded). APPLY=$APPLY"
while IFS=$'\t' read -r id sha branch; do
  [[ -z "$id" ]] && continue
  payload=$(printf '{"branch":"%s","merge_commit_sha":"%s"}' "$branch" "$sha")
  if [[ $APPLY -eq 1 ]]; then
    "$GO" event emit --kind task.landed --project endless \
      --entity-type task --entity-id "$id" \
      --actor-kind system --actor-id backfill --node-id "$NODE" \
      --project-root "$MAIN" --payload "$payload"
  else
    echo "DRY  E-$id  $sha  $branch"
  fi
done <<< "$DATA"

if [[ $APPLY -eq 1 ]]; then
  echo "Done. Verify: $GO event validate-db --project-root \"$MAIN\"  (or endless task landed <id>)"
fi
```

The script is dry-run verified (177 candidates print; E-1209 → `6671bca9`, E-1108 absent). A
runnable copy is also in this session's scratchpad as `e-1715-backfill.sh`.
