package notary

import (
	"strings"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/database"
	"github.com/nitpick-oss/nitpick/internal/database/databasetest"
	"github.com/nitpick-oss/nitpick/internal/wire"
	"github.com/nitpick-oss/nitpick/internal/wire/storetest"
)

func TestTheDatabaseStoreMeetsTheSuiteOnEachEngine(t *testing.T) {
	databasetest.Each(t, Models(), func(t *testing.T, db *database.DB) {
		fresh := map[*testing.T]bool{}
		storetest.Run(t, func(t *testing.T) wire.Store {
			// The first store a behaviour asks for starts from an empty database; a
			// second one, standing for another server, shares it.
			if !fresh[t] {
				fresh[t] = true
				for _, table := range []string{"notary_records", "notary_history"} {
					if err := db.Exec("DELETE FROM " + table).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			return NewDBStore(db)
		}, storetest.Options{MultiServer: true})
	})
}

func TestAClearKeepsTheRecordInHistoryWithItsCount(t *testing.T) {
	databasetest.Each(t, Models(), func(t *testing.T, db *database.DB) {
		s := NewDBStore(db)
		ctx := t.Context()
		r := wire.Record{Schema: wire.Schema, Hash: storetest.Hash(1), Complete: true, Batches: 1, Answers: map[string]string{"a#0": "kept"}}
		if err := s.Put(ctx, r); err != nil {
			t.Fatal(err)
		}
		for _, by := range []string{"alice", "bob"} {
			if err := s.Clear(ctx, r.Hash, by); err != nil {
				t.Fatal(err)
			}
		}
		var rows []historyRow
		if err := db.Order("clears").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 || rows[0].Kind != "cleared" || rows[0].Clears != 1 || rows[1].Clears != 2 {
			t.Fatalf("history = %+v, want two cleared rows counted 1 and 2", rows)
		}
		if !strings.Contains(string(rows[0].Body), "kept") {
			t.Errorf("the first history row lost the answers the clear removed: %s", rows[0].Body)
		}
	})
}

func TestARecordThatCannotBeDecodedIsAnErrorToReadAndIsSetAsideToWriteOver(t *testing.T) {
	databasetest.Each(t, Models(), func(t *testing.T, db *database.DB) {
		s := NewDBStore(db)
		ctx := t.Context()
		good := wire.Record{Schema: wire.Schema, Hash: storetest.Hash(1), Complete: true, Batches: 1, Answers: map[string]string{"a#0": "x"}}
		if err := s.Put(ctx, good); err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&recordRow{}).Where("hash = ?", good.Hash).Update("body", []byte("{not json")).Error; err != nil {
			t.Fatal(err)
		}
		if _, ok, err := s.Get(ctx, good.Hash); err == nil || ok {
			t.Fatalf("a record that cannot be decoded was read as %v, %v", ok, err)
		}
		// Writing over it must work, or the hash is stuck until someone edits the table.
		if err := s.Put(ctx, good); err != nil {
			t.Fatalf("a corrupt record wedged its hash: %v", err)
		}
		if got, ok, err := s.Get(ctx, good.Hash); err != nil || !ok || !got.Complete {
			t.Errorf("after writing over it: %+v, %v, %v", got, ok, err)
		}
		var kept []historyRow
		db.Where("kind = ?", "corrupt").Find(&kept)
		if len(kept) != 1 || string(kept[0].Body) != "{not json" {
			t.Errorf("the corrupt body was not kept as evidence: %+v", kept)
		}
		// A clear of one removes it and keeps the evidence.
		_ = db.Model(&recordRow{}).Where("hash = ?", good.Hash).Update("body", []byte("{also not json")).Error
		if err := s.Clear(ctx, good.Hash, "alice"); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := s.Get(ctx, good.Hash); err != nil || ok {
			t.Errorf("a cleared corrupt record is still there: %v, %v", ok, err)
		}
	})
}

func TestARecordWithAnyTextInItsFieldsRoundTripsOnBothEngines(t *testing.T) {
	databasetest.Each(t, Models(), func(t *testing.T, db *database.DB) {
		s := NewDBStore(db)
		ctx := t.Context()
		r := wire.Record{Schema: wire.Schema, Hash: storetest.Hash(7), Complete: true, Batches: 1,
			Model: "m/é💥", Tool: strings.Repeat("v", 600), Source: "ci\x00x",
			Answers: map[string]string{"a#0": "日本語   💥 \"quoted\" \\ back"}}
		if err := s.Put(ctx, r); err != nil {
			t.Fatal(err)
		}
		got, ok, err := s.Get(ctx, r.Hash)
		if err != nil || !ok || got.Model != r.Model || got.Answers["a#0"] != r.Answers["a#0"] || got.Tool != r.Tool {
			t.Errorf("round trip: %+v, %v, %v", got, ok, err)
		}
		// A name longer than its column is still stored whole in the body, and
		// the listing's copy is cut, not an error.
		page, _, err := s.List(ctx, wire.ListQuery{})
		if err != nil || len(page) != 1 || len(page[0].Tool) > 400 {
			t.Errorf("listing a record with a %d-byte version: %v, %v", len(r.Tool), page, err)
		}
	})
}
