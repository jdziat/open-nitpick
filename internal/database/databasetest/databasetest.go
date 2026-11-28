// Package databasetest gives a test a fresh, empty database on each real engine.
//
// The engines come from the environment, as a URL to a server the test may create
// databases on:
//
//	NITPICK_TEST_POSTGRES_URL=postgres://postgres:dev@127.0.0.1:55432/postgres?sslmode=disable
//	NITPICK_TEST_MYSQL_URL=mysql://root:dev@127.0.0.1:53306/mysql
//
// With neither set a test is skipped, which is right on a machine with no database
// and wrong in CI, where a skipped database test is a test that did not run.
// NITPICK_REQUIRE_DATABASES=1 turns the skip into a failure, and CI sets it.
package databasetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/database"
)

var engines = []struct {
	name database.Dialect
	env  string
}{
	{database.Postgres, "NITPICK_TEST_POSTGRES_URL"},
	{database.MySQL, "NITPICK_TEST_MYSQL_URL"},
}

// Sub is one engine's subtest: the *testing.T of that subtest, and the database
// made for it. A test body takes a Sub and not a *testing.T so that it is a test
// and not a helper, and a failure in it reports the line that failed.
type Sub struct {
	*testing.T
	DB *database.DB
}

// Each runs fn once per configured engine, as a subtest named for it, with a new
// database that is dropped afterwards. Models are migrated before fn runs.
func Each(t *testing.T, models []any, fn func(t *Sub)) {
	t.Helper()
	ran := 0
	for _, e := range engines {
		raw := strings.TrimSpace(os.Getenv(e.env))
		if raw == "" {
			if os.Getenv("NITPICK_REQUIRE_DATABASES") == "1" {
				t.Fatalf("NITPICK_REQUIRE_DATABASES=1 and %s is not set", e.env)
			}
			continue
		}
		ran++
		t.Run(string(e.name), func(t *testing.T) {
			db := fresh(t, e.name, raw)
			if len(models) > 0 {
				if err := db.Migrate(context.Background(), models...); err != nil {
					t.Fatalf("migrate: %v", err)
				}
			}
			fn(&Sub{T: t, DB: db})
		})
	}
	if ran == 0 {
		t.Skip("no test database: set NITPICK_TEST_POSTGRES_URL or NITPICK_TEST_MYSQL_URL")
	}
}

// One returns a fresh database on one engine, for a test that is about something
// other than the engines. NITPICK_TEST_ENGINE picks it (postgres or mysql, default
// postgres), so CI runs the whole suite once on each. A test that is about how the
// engines differ uses Each.
func One(tb testing.TB, models []any) *database.DB {
	tb.Helper()
	want := strings.ToLower(strings.TrimSpace(os.Getenv("NITPICK_TEST_ENGINE")))
	if want == "" {
		want = string(database.Postgres)
	}
	for _, e := range engines {
		if string(e.name) != want {
			continue
		}
		raw := strings.TrimSpace(os.Getenv(e.env))
		if raw == "" {
			if os.Getenv("NITPICK_REQUIRE_DATABASES") == "1" {
				tb.Fatalf("NITPICK_REQUIRE_DATABASES=1 and %s is not set", e.env)
			}
			tb.Skipf("no test database: set %s", e.env)
		}
		db := fresh(tb, e.name, raw)
		if len(models) > 0 {
			if err := db.Migrate(context.Background(), models...); err != nil {
				tb.Fatalf("migrate: %v", err)
			}
		}
		return db
	}
	tb.Fatalf("NITPICK_TEST_ENGINE=%q is not postgres or mysql", want)
	return nil
}

// URL returns the URL of a fresh, empty database on one engine, chosen as One
// chooses, for a test that starts a server and hands it a URL. The database is
// dropped when the test ends; nothing is migrated.
func URL(tb testing.TB) string {
	tb.Helper()
	want := strings.ToLower(strings.TrimSpace(os.Getenv("NITPICK_TEST_ENGINE")))
	if want == "" {
		want = string(database.Postgres)
	}
	for _, e := range engines {
		if string(e.name) != want {
			continue
		}
		raw := strings.TrimSpace(os.Getenv(e.env))
		if raw == "" {
			if os.Getenv("NITPICK_REQUIRE_DATABASES") == "1" {
				tb.Fatalf("NITPICK_REQUIRE_DATABASES=1 and %s is not set", e.env)
			}
			tb.Skipf("no test database: set %s", e.env)
		}
		_, u := freshURL(tb, e.name, raw)
		return u
	}
	tb.Fatalf("NITPICK_TEST_ENGINE=%q is not postgres or mysql", want)
	return ""
}

// fresh creates a database with a random name on the server raw points at, opens
// it, and drops it when the test ends.
func fresh(tb testing.TB, engine database.Dialect, raw string) *database.DB {
	tb.Helper()
	db, _ := freshURL(tb, engine, raw)
	return db
}

func freshURL(tb testing.TB, engine database.Dialect, raw string) (*database.DB, string) {
	tb.Helper()
	ctx := context.Background()
	admin, err := database.Open(ctx, raw)
	if err != nil {
		tb.Fatalf("reach the %s test server: %v", engine, err)
	}
	var b [6]byte
	_, _ = rand.Read(b[:])
	name := "nitpick_test_" + hex.EncodeToString(b[:])
	if err := admin.Exec("CREATE DATABASE " + name).Error; err != nil {
		_ = admin.Close()
		tb.Fatalf("create %s: %v", name, err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		tb.Fatal(err)
	}
	u.Path = "/" + name
	db, err := database.Open(ctx, u.String())
	if err != nil {
		_ = admin.Close()
		tb.Fatalf("open %s: %v", name, err)
	}
	tb.Cleanup(func() {
		_ = db.Close()
		// Postgres refuses to drop a database that has connections, and pool
		// connections close asynchronously, so force it.
		drop := "DROP DATABASE IF EXISTS " + name
		if engine == database.Postgres {
			drop += " WITH (FORCE)"
		}
		_ = admin.Exec(drop).Error
		_ = admin.Close()
	})
	return db, u.String()
}
