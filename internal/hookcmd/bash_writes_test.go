package hookcmd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/refusal"
)

// E-940: the Bash write-target gate. These pin the extraction (what a command
// writes, and where) and the equivalence that makes it "the same decision" as
// the Write/Edit gate.

func TestLexShell(t *testing.T) {
	home, _ := os.UserHomeDir()
	type tok struct {
		op, val string
		cut     int
	}
	w := func(v string) tok { return tok{val: v, cut: -1} }
	o := func(op string) tok { return tok{op: op} }
	cases := []struct {
		name string
		cmd  string
		want []tok
		ok   bool
	}{
		{"plain words", "echo a b", []tok{w("echo"), w("a"), w("b")}, true},
		{"single quotes keep operators", `echo '> f; x'`, []tok{w("echo"), w("> f; x")}, true},
		{"double quotes and escapes", `echo "a \"b\"" c\ d`, []tok{w("echo"), w(`a "b"`), w("c d")}, true},
		{"redirect", "echo x > f", []tok{w("echo"), w("x"), o(">"), w("f")}, true},
		{"append", "echo x >>f", []tok{w("echo"), w("x"), o(">>"), w("f")}, true},
		{"clobber", "echo x >| f", []tok{w("echo"), w("x"), o(">|"), w("f")}, true},
		{"both streams", "cmd &> f", []tok{w("cmd"), o("&>"), w("f")}, true},
		{"both streams append", "cmd &>> f", []tok{w("cmd"), o("&>>"), w("f")}, true},
		{"fd redirect", "cmd 2> f", []tok{w("cmd"), o(">"), w("f")}, true},
		{"fd dup", "cmd 2>&1", []tok{w("cmd"), o(">&"), w("1")}, true},
		{"pipe stderr", "a |& b", []tok{w("a"), o("|&"), w("b")}, true},
		{"and or", "a && b || c", []tok{w("a"), o("&&"), w("b"), o("||"), w("c")}, true},
		{"background", "a & b", []tok{w("a"), o("&"), w("b")}, true},
		{"newline separates", "a\nb", []tok{w("a"), o(";"), w("b")}, true},
		{"subshell", "(cd x; y)", []tok{o("("), w("cd"), w("x"), o(";"), w("y"), o(")")}, true},
		{"heredoc operator", "cat > f <<'EOF'", []tok{w("cat"), o(">"), w("f"), o("<<"), w("EOF")}, true},
		{"comment", "a # > f", []tok{w("a")}, true},
		{"home var", "echo $HOME/x", []tok{w("echo"), w(home + "/x")}, true},
		{"tilde", "echo ~/x", []tok{w("echo"), w(home + "/x")}, true},
		{"dynamic", `echo "$OUT"`, []tok{w("echo"), {val: "", cut: 0}}, true},
		{"dynamic after prefix", "echo /a/$X/b", []tok{w("echo"), {val: "/a//b", cut: 3}}, true},
		{"command substitution", "echo $(pwd)/f", []tok{w("echo"), {val: "/f", cut: 0}}, true},
		{"glob", "rm a/*.go", []tok{w("rm"), {val: "a/*.go", cut: 2}}, true},
		{"unterminated single", "echo 'x", nil, false},
		{"unterminated double", `echo "x`, nil, false},
		{"unterminated substitution", "echo $(x", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			toks, ok := lexShell(c.cmd)
			if ok != c.ok {
				t.Fatalf("lexShell(%q) ok = %v, want %v", c.cmd, ok, c.ok)
			}
			if !ok {
				return
			}
			var got []tok
			for _, k := range toks {
				got = append(got, tok{op: k.op, val: k.val, cut: map[bool]int{true: 0, false: k.cut}[k.op != ""]})
			}
			for i := range c.want {
				if c.want[i].op != "" {
					c.want[i].cut = 0
				}
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("lexShell(%q)\n got  %+v\n want %+v", c.cmd, got, c.want)
			}
		})
	}
}

func TestBashWriteTargets(t *testing.T) {
	home, _ := os.UserHomeDir()
	const cwd = "/wt"
	// No git repo is consulted: the seam answers "/repo" for any directory.
	orig := gitToplevel
	gitToplevel = func(dir string) string { return "/repo" }
	t.Cleanup(func() { gitToplevel = orig })

	type bw = bashWrite
	cases := []struct {
		name string
		cmd  string
		want []bw
	}{
		// Redirects.
		{"redirect relative", "echo x > f", []bw{{"/wt/f", ">", false}}},
		{"redirect absolute", "echo x > /main/f", []bw{{"/main/f", ">", false}}},
		{"append", "echo x >> /main/f", []bw{{"/main/f", ">>", false}}},
		{"clobber", "echo x >| /main/f", []bw{{"/main/f", ">|", false}}},
		{"both streams", "cmd &> /main/log", []bw{{"/main/log", "&>", false}}},
		{"fd", "cmd 2>> /main/err", []bw{{"/main/err", ">>", false}}},
		{"fd dup is not a file", "cmd 2>&1", nil},
		{"csh-style both streams", "cmd >& /main/log", []bw{{"/main/log", ">&", false}}},
		{"heredoc to file", "cat > /main/f <<'EOF'\nbody > x\nEOF", []bw{{"/main/f", ">", false}}},
		{"heredoc body is not a command", "cat <<EOF\n> /main/f\nEOF", nil},
		{"pipe to tee", "cmd 2>&1 | tee log", []bw{{"/wt/log", "tee", false}}},
		{"dev null", "cmd > /dev/null", []bw{{"/dev/null", ">", false}}},
		{"input redirect is a read", "sort < /main/f", nil},
		{"mention in quotes", `echo "> /main/f"`, nil},
		{"sed mention", `echo "sed -i x /main/f"`, nil},
		{"string compare in [[", `[[ "$a" > b ]]`, nil},
		{"arithmetic", "(( a > b ))", nil},

		// tee, sed, perl.
		{"tee -a", "tee -a /main/a /main/b", []bw{{"/main/a", "tee", false}, {"/main/b", "tee", false}}},
		{"sed -i", "sed -i 's/a/b/' /main/f", []bw{{"/main/f", "sed -i", false}}},
		{"sed -i suffix", "sed -i.bak 's/a/b/' /main/f", []bw{{"/main/f", "sed -i", false}}},
		{"sed -i bsd", "sed -i '' 's/a/b/' /main/f", []bw{{"/main/f", "sed -i", false}}},
		{"sed --in-place", "sed --in-place=.b -e s/a/b/ /main/f /main/g", []bw{{"/main/f", "sed -i", false}, {"/main/g", "sed -i", false}}},
		{"sed -e then -i", "sed -e s/a/b/ -i /main/f", []bw{{"/main/f", "sed -i", false}}},
		{"sed without -i", "sed 's/a/b/' /main/f", nil},
		{"perl -pi -e", "perl -pi -e 's/a/b/' /main/f", []bw{{"/main/f", "perl -i", false}}},
		{"perl -i.bak", "perl -i.bak -pe 's/a/b/' /main/f", []bw{{"/main/f", "perl -i", false}}},
		{"perl -e is not in place", "perl -e 'print 1' /main/f", nil},

		// File utilities.
		{"mv", "mv a /main/b", []bw{{"/wt/a", "mv", false}, {"/main/b", "mv", false}}},
		{"mv -t", "mv -t /main a", []bw{{"/main", "mv", false}, {"/wt/a", "mv", false}}},
		{"cp dest only", "cp /main/x ./x", []bw{{"/wt/x", "cp", false}}},
		{"cp into main", "cp x /main/x", []bw{{"/main/x", "cp", false}}},
		{"cp -t", "cp --target-directory=/main a b", []bw{{"/main", "cp", false}}},
		{"ln -s", "ln -s /wt/x /main/y", []bw{{"/main/y", "ln", false}}},
		{"install -d", "install -d /main/d", []bw{{"/main/d", "install -d", false}}},
		{"rsync remote dest", "rsync -a x host:/y", nil},
		{"rm", "rm -rf /main/a b", []bw{{"/main/a", "rm", false}, {"/wt/b", "rm", false}}},
		{"touch -r reads ref", "touch -r /main/ref f", []bw{{"/wt/f", "touch", false}}},
		{"mkdir -p", "mkdir -p /main/d", []bw{{"/main/d", "mkdir", false}}},
		{"truncate -s", "truncate -s 0 /main/f", []bw{{"/main/f", "truncate", false}}},
		{"chmod", "chmod +x /main/f", []bw{{"/main/f", "chmod", false}}},
		{"chown -R", "chown -R u:g /main/d", []bw{{"/main/d", "chown", false}}},
		{"dd of=", "dd if=/dev/zero of=/main/f bs=1", []bw{{"/main/f", "dd of=", false}}},

		// Formatters, downloads, archives.
		{"gofmt -w", "gofmt -w /main/a.go", []bw{{"/main/a.go", "gofmt -w", false}}},
		{"gofmt -l is a read", "gofmt -l /main/a.go", nil},
		{"goimports -w", "goimports -w -local x /main/a.go", []bw{{"/main/a.go", "goimports -w", false}}},
		{"prettier --write", "prettier --write /main/a.ts", []bw{{"/main/a.ts", "prettier --write", false}}},
		{"ruff format", "ruff format /main/a.py", []bw{{"/main/a.py", "ruff format", false}}},
		{"ruff format cwd", "ruff format", []bw{{"/wt", "ruff format", false}}},
		{"black", "black /main/a.py", []bw{{"/main/a.py", "black", false}}},
		{"black --check", "black --check /main/a.py", nil},
		{"curl -o", "curl -sSL -o /main/f https://x", []bw{{"/main/f", "curl -o", false}}},
		{"curl -sSLo", "curl -sSLo /main/f https://x", []bw{{"/main/f", "curl -o", false}}},
		{"curl -o -", "curl -o - https://x", nil},
		{"curl -O", "curl -O https://x/f", []bw{{"/wt", "curl -O", false}}},
		{"wget -O", "wget -O /main/f https://x", []bw{{"/main/f", "wget -O", false}}},
		{"tar -x -C", "tar -xzf a.tgz -C /main", []bw{{"/main", "tar -x", false}}},
		{"tar old-style", "tar xzf a.tgz", []bw{{"/wt", "tar -x", false}}},
		{"tar create is a read", "tar -czf a.tgz -C /main .", nil},
		{"unzip -d", "unzip a.zip -d /main", []bw{{"/main", "unzip -d", false}}},
		{"unzip -l", "unzip -l a.zip", nil},
		{"patch file", "patch -p1 /main/f < x.diff", []bw{{"/main/f", "patch", false}}},
		{"patch -d", "patch -d /main -p1 -i x.diff", []bw{{"/main", "patch -d", false}}},
		{"find -delete", "find /main/tmp -name '*.o' -delete", []bw{{"/main/tmp", "find -delete", false}}},
		{"find -exec rm", "find /main -name x -exec rm {} +", []bw{{"/main", "find -delete", false}}},
		{"find is a read", "find /main -name x", nil},

		// Directory tracking.
		{"cd then sed", "cd /main && sed -i s/a/b/ f", []bw{{"/main/f", "sed -i", false}}},
		{"relative cd", "cd sub && touch f", []bw{{"/wt/sub/f", "touch", false}}},
		{"subshell scoping", "(cd /main; touch f); touch g", []bw{{"/main/f", "touch", false}, {"/wt/g", "touch", false}}},
		{"pushd popd", "pushd /main && touch f && popd && touch g", []bw{{"/main/f", "touch", false}, {"/wt/g", "touch", false}}},
		{"cd to unknown", `cd "$X" && touch f`, nil},
		{"cd to unknown, absolute still judged", `cd "$X" && touch /main/f`, []bw{{"/main/f", "touch", false}}},

		// Wrappers and recursion.
		{"env assignment", "FOO=1 sudo -u root rm /main/f", []bw{{"/main/f", "rm", false}}},
		{"time nohup", "time nohup touch /main/f", []bw{{"/main/f", "touch", false}}},
		{"bash -c", `bash -c "echo x > /main/f"`, []bw{{"/main/f", ">", false}}},
		{"sh -ec", `sh -ec 'cd /main && touch f'`, []bw{{"/main/f", "touch", false}}},
		{"bash -c scopes cd", `bash -c 'cd /main' && touch f`, []bw{{"/wt/f", "touch", false}}},
		{"eval", `eval "touch /main/f"`, []bw{{"/main/f", "touch", false}}},
		{"xargs is opaque", "ls | xargs rm", nil},

		// Dynamic and glob targets.
		{"dynamic target", `echo x > "$OUT"`, []bw{{"", ">", true}}},
		{"dynamic behind absolute prefix", `echo x > /main/$N.log`, []bw{{"/main", ">", true}}},
		{"home var", `touch $HOME/x`, []bw{{home + "/x", "touch", false}}},
		{"home reassigned in the command", `HOME=/tmp/h; mkdir -p "$HOME"`, []bw{{"", "mkdir", true}}},
		{"home exported in the command", `export HOME=/tmp/h && touch ~/x`, []bw{{"", "touch", true}}},
		{"tilde glob", "rm ~/x/*.go", []bw{{home + "/x", "rm", true}}},
		{"relative glob", "rm .endless/tmp/*.md", []bw{{"/wt/.endless/tmp", "rm", true}}},
		{"command substitution target", "echo x > $(mktemp)", []bw{{"", ">", true}}},

		// Not writes.
		{"git commit", `git commit -m "a > b"`, nil},
		{"git add", "git add -A", nil},
		{"git status", "git -C /main status", nil},
		{"endless", "endless task update E-1 --plan-file .endless/tmp/p.md", nil},
		{"python -c", `python -c "open('f','w')"`, nil},
		{"unterminated quote", `echo 'x > /main/f`, nil},

		// Git working-tree mutators (target = the repo top, from the seam).
		{"git restore", "git restore .", []bw{{"/repo", "git restore", false}}},
		{"git -C restore", "git -C /main restore .", []bw{{"/repo", "git restore", false}}},
		{"git checkout --", "git checkout -- f", []bw{{"/repo", "git checkout", false}}},
		{"git reset --hard", "git reset --hard main", []bw{{"/repo", "git reset", false}}},
		{"git reset soft", "git reset HEAD~1", nil},
		{"git stash pop", "git stash pop", []bw{{"/repo", "git stash", false}}},
		{"git stash list", "git stash list", nil},
		{"git clean -n", "git clean -n", nil},
		{"git clean -fd", "git clean -fd", []bw{{"/repo", "git clean", false}}},
		{"git apply --check", "git apply --check x.diff", nil},
		{"git pull", "git pull", []bw{{"/repo", "git pull", false}}},
		{"git restore --staged", "git restore --staged f", nil},
		{"git --work-tree", "git --work-tree=/main checkout -- f", []bw{{"/main", "git checkout", false}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := bashWriteTargets(c.cmd, cwd, true)
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("bashWriteTargets(%q)\n got  %+v\n want %+v", c.cmd, got, c.want)
			}
		})
	}

	t.Run("git mutators off when the check is off", func(t *testing.T) {
		if got := bashWriteTargets("git restore .", cwd, false); len(got) != 0 {
			t.Errorf("got %+v with %s off, want none", got, gitWritesCheck)
		}
	})
}

// writeFixture is a project with a claimed worktree, all symlink-resolved.
type writeFixture struct {
	main, wt, other, tmp string
	scope                writeScope
}

func newWriteFixture(t *testing.T) writeFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := writeFixture{
		main:  filepath.Join(root, "proj"),
		wt:    filepath.Join(root, "proj", ".endless", "worktrees", "e-7"),
		other: filepath.Join(root, "proj", ".endless", "worktrees", "e-8"),
		tmp:   filepath.Join(root, "scratch"),
	}
	admin := filepath.Join(f.main, ".git", "worktrees", "e-7")
	for _, d := range []string{f.wt, f.other, f.tmp, admin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A linked worktree's `.git` file and its admin dir's `commondir`, as git
	// writes them.
	if err := os.WriteFile(filepath.Join(f.wt, ".git"), []byte("gitdir: "+admin+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(admin, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.scope = writeScope{
		taskID:      7,
		worktree:    f.wt,
		projectRoot: f.main,
		gitDirs:     worktreeGitDirs(f.wt),
		exempt:      []string{f.tmp, root}, // root is "the temp dir" the project lives in
		landed: func(id int64) (*refusal.Error, bool) {
			if id == 101 {
				// A classified stand-in: the gate under test only cares that
				// the landed-suite arm fires and that its refusal is what comes
				// back, not what class it carries.
				return refusal.NoReport("LANDED-SUITE-REFUSAL", "stub remedy"), true
			}
			return nil, false
		},
	}
	return f
}

func TestWriteTargetDecision(t *testing.T) {
	f := newWriteFixture(t)
	noTask := f.scope
	noTask.worktree, noTask.taskID = "", 0

	cases := []struct {
		name    string
		scope   writeScope
		target  string
		block   bool
		contain string
	}{
		{"own worktree", f.scope, filepath.Join(f.wt, "a.go"), false, ""},
		{"own worktree root", f.scope, f.wt, false, ""},
		{"main checkout", f.scope, filepath.Join(f.main, "a.go"), true, "outside your worktree"},
		{"another task's worktree", f.scope, filepath.Join(f.other, "a.go"), true, "E-7"},
		{"temp dir", f.scope, filepath.Join(f.tmp, "x"), false, ""},
		{"temp dir holding the project is not an exemption for it", f.scope, filepath.Join(f.main, "x"), true, ""},
		{"own git admin dir", f.scope, filepath.Join(f.main, ".git/worktrees/e-7/index.lock"), false, ""},
		{"endless's git-side state", f.scope, filepath.Join(f.main, ".git/info/endless/unlanded/x"), false, ""},
		{"another worktree's admin dir", f.scope, filepath.Join(f.main, ".git/worktrees/e-8/index.lock"), true, ""},
		{"shared git config", f.scope, filepath.Join(f.main, ".git/config"), true, ""},
		{"dev null", f.scope, "/dev/null", false, ""},
		{"dev fd", f.scope, "/dev/fd/3", false, ""},
		{"unrelated path", f.scope, "/etc/hosts", true, ""},
		{"doc mirror in worktree", f.scope, filepath.Join(f.wt, ".endless/tasks/e-7/plan.md"), true, "document mirror"},
		{"own verify suite", f.scope, filepath.Join(f.wt, ".endless/tasks/e-7/verify.sh"), false, ""},
		{"landed foreign suite", f.scope, filepath.Join(f.wt, ".endless/tasks/e-101/verify.sh"), true, "LANDED-SUITE-REFUSAL"},
		{"no claimed worktree: main allowed", noTask, filepath.Join(f.main, "a.go"), false, ""},
		{"no claimed worktree: mirror still refused", noTask, filepath.Join(f.main, ".endless/tasks/e-7/plan.md"), true, "document mirror"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg, block := writeTargetDecision(c.scope, c.target)
			if block != c.block {
				t.Fatalf("writeTargetDecision(%s) block = %v, want %v\n%s", c.target, block, c.block, msg)
			}
			if c.contain != "" && !strings.Contains(msg.Error(), c.contain) {
				t.Errorf("message lacks %q:\n%s", c.contain, msg)
			}
		})
	}
}

// TestResolveWriteTarget pins the symlink handling the containment test depends
// on: a target that does not exist yet still resolves through its nearest
// existing ancestor.
func TestResolveWriteTarget(t *testing.T) {
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, cwd, in, want string }{
		{"absolute through a symlink, not yet existing", "", filepath.Join(link, "new", "f"), filepath.Join(real, "new", "f")},
		{"relative against cwd", link, "sub/f", filepath.Join(real, "sub", "f")},
		{"relative with no cwd stays relative", "", "./.endless/x", ".endless/x"},
		{"empty", "/x", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveWriteTarget(c.cwd, c.in); got != c.want {
				t.Errorf("resolveWriteTarget(%q, %q) = %q, want %q", c.cwd, c.in, got, c.want)
			}
		})
	}
}

// TestBashWriteGateMatchesWriteGate is the equivalence property that keeps "the
// same decision" true over time: for each target, a Write to it and
// `echo x > <target>` get the same answer, and the Bash refusal carries the
// Write refusal's body verbatim.
func TestBashWriteGateMatchesWriteGate(t *testing.T) {
	f := newWriteFixture(t)
	targets := []string{
		filepath.Join(f.wt, "a.go"),
		filepath.Join(f.main, "a.go"),
		filepath.Join(f.other, "a.go"),
		filepath.Join(f.tmp, "x"),
		"/dev/null",
		filepath.Join(f.wt, ".endless/tasks/e-7/plan.md"),
		filepath.Join(f.wt, ".endless/tasks/e-101/verify.sh"),
		"/etc/hosts",
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			writeMsg, writeBlock := writeTargetDecision(f.scope, resolveWriteTarget(f.wt, target))
			bashMsg, bashBlock := bashWritesRefusal(f.scope, bashWriteTargets("echo x > "+target, f.wt, true))
			if writeBlock != bashBlock {
				t.Fatalf("Write block=%v, Bash block=%v", writeBlock, bashBlock)
			}
			if writeBlock && !strings.HasSuffix(bashMsg.Error(), writeMsg.Error()) {
				t.Errorf("Bash refusal does not carry the Write refusal's body:\nWrite:\n%s\nBash:\n%s", writeMsg, bashMsg)
			}
			if bashBlock && !strings.HasPrefix(bashMsg.Error(), "`>` would write ") {
				t.Errorf("Bash refusal does not name the construct:\n%s", bashMsg)
			}
		})
	}
}

func TestBashWritesRefusalNamesGitCheck(t *testing.T) {
	f := newWriteFixture(t)
	msg, block := bashWritesRefusal(f.scope, []bashWrite{{Path: f.main, Construct: "git restore"}})
	if !block {
		t.Fatal("git restore in main was allowed")
	}
	if !strings.Contains(msg.Error(), `"`+gitWritesCheck+`": false`) {
		t.Errorf("git refusal does not name the %s key:\n%s", gitWritesCheck, msg)
	}
}
