package database_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/nitpick-oss/nitpick/internal/database"
	"github.com/nitpick-oss/nitpick/internal/database/databasetest"
)

type thing struct {
	ID    uint   `gorm:"primaryKey"`
	Name  string `gorm:"size:100;uniqueIndex"`
	Count int
}

func TestOpenRefusesWhatItCannotUse(t *testing.T) {
	for name, tc := range map[string]struct{ url, want string }{
		"no url":        {"", "no database URL"},
		"a file":        {"sqlite:///tmp/x.db", "not postgres or mysql"},
		"mysql no host": {"mysql:///db", "no host"},
		"mysql no db":   {"mysql://u:p@localhost:3306/", "no database"},
		"not a url":     {"::::", "not a URL"},
	} {
		_, err := database.Open(context.Background(), tc.url)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one naming %q", name, err, tc.want)
		}
	}
}

func TestAnErrorNeverRepeatsTheDatabasePassword(t *testing.T) {
	const secret = "hunter2-very-secret"
	for _, raw := range []string{
		"postgres://u:" + secret + "@127.0.0.1:1/db?sslmode=disable&connect_timeout=1",
		"mysql://u:" + secret + "@127.0.0.1:1/db",
	} {
		_, err := database.Open(context.Background(), raw)
		if err == nil {
			t.Fatalf("%s: connected to a closed port", raw[:8])
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("the error repeats the password: %v", err)
		}
	}
}

func TestMigrateRunsOnceWhenManyServersStartTogether(t *testing.T) {
	databasetest.Each(t, nil, func(t *databasetest.Sub) {
		db := t.DB
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- db.Migrate(context.Background(), &thing{})
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Errorf("a concurrent migration failed: %v", err)
			}
		}
		if !db.Migrator().HasTable(&thing{}) {
			t.Error("the table was not created")
		}
	})
}

func TestMigrateOnlyAddsAndKeepsWhatIsThere(t *testing.T) {
	type v1 struct {
		ID   uint `gorm:"primaryKey"`
		Name string
	}
	type v2 struct {
		ID    uint `gorm:"primaryKey"`
		Name  string
		Extra string
	}
	databasetest.Each(t, nil, func(t *databasetest.Sub) {
		db := t.DB
		if err := db.Table("t").AutoMigrate(&v1{}); err != nil {
			t.Fatal(err)
		}
		if err := db.Table("t").Create(&v1{Name: "kept"}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(context.Background(), &thing{}); err != nil {
			t.Fatal(err)
		}
		if err := db.Table("t").AutoMigrate(&v2{}); err != nil {
			t.Fatal(err)
		}
		var got v2
		if err := db.Table("t").First(&got).Error; err != nil || got.Name != "kept" {
			t.Errorf("row after adding a column = %+v, %v", got, err)
		}
	})
}

func TestTransactRunsAgainAfterADeadlockAndStopsOnAnyOtherError(t *testing.T) {
	databasetest.Each(t, []any{&thing{}}, func(t *databasetest.Sub) {
		db := t.DB
		ctx := context.Background()
		if err := db.Create(&thing{Name: "a"}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&thing{Name: "b"}).Error; err != nil {
			t.Fatal(err)
		}
		// Two transactions take two rows in opposite order until one is chosen as a
		// deadlock victim. Transact must run the victim again so both finish.
		var wg sync.WaitGroup
		results := make(chan error, 2)
		start := make(chan struct{})
		for _, order := range [][2]string{{"a", "b"}, {"b", "a"}} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				results <- db.Transact(ctx, func(tx *gorm.DB) error {
					for _, name := range order {
						if err := tx.Exec("UPDATE things SET count = count + 1 WHERE name = ?", name).Error; err != nil {
							return err
						}
						time.Sleep(40 * time.Millisecond)
					}
					return nil
				})
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Errorf("a transaction was left failed by a deadlock: %v", err)
			}
		}
		var sum int
		db.Raw("SELECT SUM(count) FROM things").Scan(&sum)
		if sum != 4 {
			t.Errorf("each of two transactions adds one to two rows: sum = %d, want 4", sum)
		}

		calls := 0
		err := db.Transact(ctx, func(*gorm.DB) error { calls++; return errors.New("not a deadlock") })
		if err == nil || calls != 1 {
			t.Errorf("a plain error ran %d times (err %v), want once", calls, err)
		}
	})
}

func TestADuplicateKeyIsRecognisedOnBothEngines(t *testing.T) {
	databasetest.Each(t, []any{&thing{}}, func(t *databasetest.Sub) {
		db := t.DB
		if err := db.Create(&thing{Name: "x"}).Error; err != nil {
			t.Fatal(err)
		}
		err := db.Create(&thing{Name: "x"}).Error
		if !database.IsDuplicate(err) {
			t.Errorf("a repeated unique key was not recognised: %v", err)
		}
		if database.IsDuplicate(errors.New("other")) {
			t.Error("an unrelated error was taken for a duplicate")
		}
	})
}
