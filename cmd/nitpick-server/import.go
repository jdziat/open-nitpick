package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"

	"github.com/nitpick-oss/nitpick/internal/database"
	"github.com/nitpick-oss/nitpick/internal/insights"
	"github.com/nitpick-oss/nitpick/internal/notary"
	"github.com/nitpick-oss/nitpick/internal/server"
)

// runImport copies what an earlier, file-backed server kept into the database. It
// is for the move from one to the other and is safe to run again: what the database
// has is never overwritten.
func runImport(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nitpick-server import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("data-dir", "", "the data directory the file-backed server used")
	url := fs.String("database-url", os.Getenv("NITPICK_DATABASE_URL"), "the database to copy into (default $NITPICK_DATABASE_URL)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *dir == "" {
		_, _ = fmt.Fprintln(stderr, "nitpick-server import: -data-dir is required")
		return 2
	}
	db, err := database.Open(ctx, *url)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "nitpick-server import:", err)
		return 1
	}
	defer func() { _ = db.Close() }()
	var models []any
	models = append(models, notary.Models()...)
	models = append(models, insights.Models()...)
	models = append(models, server.Models()...)
	if err := db.Migrate(ctx, models...); err != nil {
		_, _ = fmt.Fprintln(stderr, "nitpick-server import:", err)
		return 1
	}
	res, err := importDir(ctx, db, *dir)
	_, _ = fmt.Fprintf(stdout, "notary records copied: %d, history copied: %d, already in the database: %d, skipped: %d\n",
		res.Notary.Records, res.Notary.History, res.Notary.Present, res.Notary.Skipped)
	for _, n := range res.Notary.Names {
		_, _ = fmt.Fprintf(stdout, "  skipped %s\n", n)
	}
	_, _ = fmt.Fprintf(stdout, "insights reviews copied: %d, already in the database: %d, skipped: %d\n",
		res.Insights.Moved, res.Insights.Present, res.Insights.Skipped)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "nitpick-server import:", err)
		return 1
	}
	return 0
}

type importResult struct {
	Notary   notary.ImportCounts
	Insights insights.ImportCounts
}

// importDir copies the notary records in dir and the insights reviews in
// dir/insights. A directory a server still holds is refused: it would keep writing
// to files this has already read.
func importDir(ctx context.Context, db *database.DB, dir string) (importResult, error) {
	var res importResult
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return res, fmt.Errorf("%s is not a directory", dir)
	}
	for _, d := range []string{dir, filepath.Join(dir, "insights")} {
		if err := refuseIfInUse(d); err != nil {
			return res, err
		}
	}
	if res.Notary, err = notary.NewDBStore(db).ImportDir(ctx, dir); err != nil {
		return res, err
	}
	f, err := os.Open(filepath.Join(dir, "insights", "reviews.jsonl"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return res, nil
	case err != nil:
		return res, err
	}
	defer func() { _ = f.Close() }()
	res.Insights, err = insights.NewStore(db).Import(ctx, f)
	return res, err
}

// refuseIfInUse fails when a process holds dir's lock file. A directory with no
// lock file, or one that cannot be locked for a reason other than being held, has
// no server writing to it.
func refuseIfInUse(dir string) error {
	p := filepath.Join(dir, ".lock")
	if _, err := os.Stat(p); err != nil {
		return nil
	}
	l := flock.New(p)
	ok, err := l.TryLock()
	if err != nil {
		return nil
	}
	if !ok {
		return fmt.Errorf("%s is in use by another process: stop the server that is writing to it first", dir)
	}
	_ = l.Unlock()
	return nil
}
