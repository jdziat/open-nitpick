package storetest_test

import (
	"testing"

	"github.com/nitpick-oss/nitpick/internal/wire"
	"github.com/nitpick-oss/nitpick/internal/wire/storetest"
)

// The file store is what the database store replaces, so it is the reference the
// suite is checked against: an expectation the file store fails is a wrong
// expectation, not a bug to fix there.
func TestTheFileStoreMeetsTheSuite(t *testing.T) {
	dirs := map[*testing.T]string{}
	storetest.Run(t, func(t *testing.T) wire.Store {
		dir, ok := dirs[t]
		if !ok {
			dir = t.TempDir()
			dirs[t] = dir
		}
		s, err := wire.NewFileStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}, storetest.Options{})
}
