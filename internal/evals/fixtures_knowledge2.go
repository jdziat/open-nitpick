package evals

import "github.com/jdziat/open-nitpick/internal/config"

// time.After in a loop holds a timer per iteration until it fires.
func knowTimeAfterLeakFixture() Fixture {
	return Fixture{
		Name: "know-go-time-after-leak",
		Base: map[string]string{"go.mod": knowGoMod, "queue/worker.go": `package queue

// Drain handles every message until the channel closes.
func Drain(ch <-chan Message, handle func(Message)) {
	for m := range ch {
		handle(m)
	}
}

// Message is one unit of work.
type Message struct{ ID string }
`},
		Head: map[string]string{"go.mod": knowGoMod, "queue/worker.go": `package queue

import "time"

// idleTimeout ends a drain that has gone quiet.
const idleTimeout = time.Hour

// Drain handles every message until the channel closes or goes quiet.
func Drain(ch <-chan Message, handle func(Message)) {
	for {
		select {
		case m, ok := <-ch:
			if !ok {
				return
			}
			handle(m)
		case <-time.After(idleTimeout):
			return
		}
	}
}

// Message is one unit of work.
type Message struct{ ID string }
`},
		Defects: []Defect{{
			Path: "queue/worker.go", Line: 17,
			Keywords: []string{"time.After", "until it fires", "not recovered",
				"garbage collect", "one per message", "NewTimer", "timer per"},
			Class:        config.ClassResource,
			WantSeverity: config.SeverityWarning,
			Why:          "each iteration leaves an hour-long timer alive, one per message",
		}},
	}
}

// The control: one timer, reset, stopped.
func knowCleanTimerStoppedFixture() Fixture {
	return Fixture{
		Name: "know-go-clean-timer-reset",
		Base: map[string]string{"go.mod": knowGoMod, "queue/worker.go": `package queue

// Drain handles every message until the channel closes.
func Drain(ch <-chan Message, handle func(Message)) {
	for m := range ch {
		handle(m)
	}
}

// Message is one unit of work.
type Message struct{ ID string }
`},
		Head: map[string]string{"go.mod": knowGoMod, "queue/worker.go": `package queue

import "time"

// idleTimeout ends a drain that has gone quiet.
const idleTimeout = time.Hour

// Drain handles every message until the channel closes or goes quiet.
func Drain(ch <-chan Message, handle func(Message)) {
	idle := time.NewTimer(idleTimeout)
	defer idle.Stop()

	for {
		select {
		case m, ok := <-ch:
			if !ok {
				return
			}
			handle(m)
			if !idle.Stop() {
				<-idle.C
			}
			idle.Reset(idleTimeout)
		case <-idle.C:
			return
		}
	}
}

// Message is one unit of work.
type Message struct{ ID string }
`},
	}
}

// A rows loop with no rows.Err truncates on a connection failure.
func knowRowsErrFixture() Fixture {
	return Fixture{
		Name: "know-go-rows-err-unchecked",
		Base: map[string]string{"go.mod": knowGoMod, "store/list.go": `package store

import "database/sql"

// Names returns every account name.
func Names(db *sql.DB) ([]string, error) {
	return nil, nil
}
`},
		Head: map[string]string{"go.mod": knowGoMod, "store/list.go": `package store

import "database/sql"

// Names returns every account name.
func Names(db *sql.DB) ([]string, error) {
	rows, err := db.Query("SELECT name FROM accounts")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}
`},
		Defects: []Defect{{
			Path: "store/list.go", Line: 20,
			Keywords: []string{"rows.Err", "iteration error", "truncat", "partial result",
				"incomplete result", "ends on error"},
			Class:        config.ClassCorrectness,
			WantSeverity: config.SeverityError,
			Why:          "the loop ends on error and on success alike, so a partial result is returned as complete",
		}},
	}
}

// The control: the same loop, with the error checked.
func knowCleanRowsErrCheckedFixture() Fixture {
	return Fixture{
		Name: "know-go-clean-rows-err-checked",
		Base: map[string]string{"go.mod": knowGoMod, "store/list.go": `package store

import "database/sql"

// Names returns every account name.
func Names(db *sql.DB) ([]string, error) {
	return nil, nil
}
`},
		Head: map[string]string{"go.mod": knowGoMod, "store/list.go": `package store

import "database/sql"

// Names returns every account name.
func Names(db *sql.DB) ([]string, error) {
	rows, err := db.Query("SELECT name FROM accounts")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
`},
	}
}
