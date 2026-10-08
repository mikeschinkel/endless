package monitor

import (
	"testing"

	"github.com/mikeschinkel/endless/internal/faults"
)

// seedRaisers seeds ES-7 working on E-40 and ES-8 bound to no task. Their Claude
// session ids are uuid-7 and uuid-8.
func seedRaisers(t *testing.T) {
	t.Helper()
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	insertTaskFull(t, db, 40, 1, 0, typeTask, "underway")
	insertSessionWithID(t, db, 7, 1)
	insertSessionWithID(t, db, 8, 1)
	if _, err := db.Exec(`UPDATE sessions SET task_id = 40 WHERE id = 7`); err != nil {
		t.Fatalf("bind ES-7: %v", err)
	}
}

const worktreeE55 = "/Users/someone/proj/.endless/worktrees/e-55/internal"

func TestResolveFaultRaiser(t *testing.T) {
	tests := []struct {
		name     string
		explicit faults.Raiser
		env      RaiserEnv
		want     faults.Raiser
	}{
		{
			name: "an agent-run fault records the session and its task",
			env:  RaiserEnv{Agent: true, ClaudeSession: "uuid-7", Cwd: "/p1"},
			want: faults.Raiser{SessionID: 7, TaskID: 40},
		},
		{
			name: "a hook's payload session counts without a detected harness",
			env:  RaiserEnv{HookSession: "uuid-7", Cwd: "/p1"},
			want: faults.Raiser{SessionID: 7, TaskID: 40},
		},
		{
			name: "a person-run fault records no session, even with esu's variable set",
			env:  RaiserEnv{ClaudeSession: "uuid-7", EndlessSession: "7", Cwd: "/p1"},
			want: faults.Raiser{},
		},
		{
			name: "an agent whose Claude session is unknown falls back to ENDLESS_SESSION_ID",
			env:  RaiserEnv{Agent: true, ClaudeSession: "uuid-missing", EndlessSession: "7", Cwd: "/p1"},
			want: faults.Raiser{SessionID: 7, TaskID: 40},
		},
		{
			name:     "an explicit task overrides the session's",
			explicit: faults.Raiser{TaskID: 99},
			env:      RaiserEnv{Agent: true, ClaudeSession: "uuid-7", Cwd: worktreeE55},
			want:     faults.Raiser{SessionID: 7, TaskID: 99},
		},
		{
			name:     "an explicit session supplies its own task",
			explicit: faults.Raiser{SessionID: 7},
			env:      RaiserEnv{Cwd: "/p1"},
			want:     faults.Raiser{SessionID: 7, TaskID: 40},
		},
		{
			name: "the worktree fills the task when there is no session",
			env:  RaiserEnv{Cwd: worktreeE55},
			want: faults.Raiser{TaskID: 55},
		},
		{
			name: "the worktree fills the task of a session bound to none",
			env:  RaiserEnv{Agent: true, ClaudeSession: "uuid-8", Cwd: worktreeE55},
			want: faults.Raiser{SessionID: 8, TaskID: 55},
		},
		{
			name: "nothing known is the zero raiser",
			env:  RaiserEnv{Agent: true, Cwd: "/p1"},
			want: faults.Raiser{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seedRaisers(t)
			if got := ResolveFaultRaiser(tt.explicit, tt.env); got != tt.want {
				t.Errorf("ResolveFaultRaiser = %+v, want %+v", got, tt.want)
			}
		})
	}
}
