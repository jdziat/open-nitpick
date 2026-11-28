package insights

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"gorm.io/gorm/clause"
)

// ImportCounts say what Import did with a reviews file. Each line lands in exactly
// one of them.
type ImportCounts struct {
	// Moved were copied into the database.
	Moved int
	// Present were already there, and were left alone.
	Present int
	// Skipped could not be read as a review, were refused as one, or were longer than
	// a review may be.
	Skipped int
}

// maxLine bounds one line of a reviews file, so a damaged file cannot make the
// import hold it all in memory.
const maxLine = 8 << 20

// Import copies the reviews of a reviews.jsonl file the file store wrote into the
// database, once. A review is kept under its id, so a second run and a review the
// server already took are counted as present and not written again.
func (s *Store) Import(ctx context.Context, f io.Reader) (ImportCounts, error) {
	var c ImportCounts
	r := bufio.NewReaderSize(f, 64<<10)
	var line []byte
	tooLong := false
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return c, nil
			}
			return c, err
		}
		if !tooLong && len(line)+len(chunk) > maxLine {
			tooLong = true
		}
		if !tooLong {
			line = append(line, chunk...)
		}
		if isPrefix {
			continue
		}
		text := bytes.TrimSpace(line)
		line2, wasLong := line[:0], tooLong
		tooLong = false
		switch {
		case wasLong:
			c.Skipped++
		case len(text) == 0:
		default:
			moved, err := s.importLine(ctx, text)
			switch {
			case errors.Is(err, ErrInvalid):
				c.Skipped++
			case err != nil:
				return c, err
			case moved:
				c.Moved++
			default:
				c.Present++
			}
		}
		line = line2
	}
}

func (s *Store) importLine(ctx context.Context, text []byte) (bool, error) {
	var rv Review
	if json.Unmarshal(text, &rv) != nil || rv.ID == "" {
		return false, invalid("not a review")
	}
	rv, err := clean(rv)
	if err != nil {
		return false, err
	}
	body, err := json.Marshal(rv)
	if err != nil {
		return false, fmt.Errorf("encode the review: %w", err)
	}
	row := storedReview{ID: rv.ID, Repo: rv.Repo, At: rv.At, Complete: rv.Complete, Body: body}
	res := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if res.Error != nil {
		return false, fmt.Errorf("store the review: %w", res.Error)
	}
	return res.RowsAffected > 0, nil
}
