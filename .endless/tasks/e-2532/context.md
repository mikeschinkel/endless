Raised during the E-2531 brainstorm. A session in go-logutil asserted that a
hook cannot reliably extract the commands a session hands the user from its
prose, but it did not look at any transcripts to check. Mike wants data
before deciding whether hooks can capture commands without the session doing
anything special. His threshold: if hooks catch 90% or more, Endless captures
them with a hook and the session keeps writing commands as it does today; the
missed 10% is no worse than now.

Sources to sample:
- Session transcripts under ~/.claude/projects/*/ (JSONL, assistant text).
- ~/.claude/logs/hook.log (very large) to see which hook events fire when a
  session emits text, and whether mid-turn text between tool calls reaches
  any hook or only the final Stop message.

Questions to answer with counts, not impressions:
1. Of commands handed to the user, what fraction sit in a fenced block, and
   what info strings do those fences carry (bash, sh, none, other)?
2. What fraction are inline code spans or bare prose?
3. How often does a fenced block hold something that is not a command
   (error text, file content, TOML, output)? What distinguishes those?
4. Which hook events carry assistant text, and does mid-turn text appear
   in any of them?
5. Given 1-4, what rule would a hook apply, and what capture rate and
   false-positive rate would that rule have had on the sample?
