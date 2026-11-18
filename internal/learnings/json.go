package learnings

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// Decode rejects ambiguous object keys, unknown fields, and trailing values so
// a reviewed JSON file has the same meaning to its reader and to the loader.
func Decode(raw []byte, target any) error {
	if len(raw) > maxManifestBytes {
		return errors.New("learning JSON exceeds one MiB")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := uniqueKeys(dec, 0); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("learning JSON contains trailing data")
	}
	dec = json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return errors.New("learning JSON is invalid or contains unknown fields")
	}
	return nil
}

func uniqueKeys(dec *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("learning JSON exceeds the nesting limit")
	}
	token, err := dec.Token()
	if err != nil {
		return errors.New("learning JSON is malformed")
	}
	switch token {
	case json.Delim('{'):
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return errors.New("learning JSON key is malformed")
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return errors.New("learning JSON contains duplicate or invalid keys")
			}
			seen[name] = true
			if err := uniqueKeys(dec, depth+1); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return errors.New("learning JSON object is incomplete")
		}
	case json.Delim('['):
		for dec.More() {
			if err := uniqueKeys(dec, depth+1); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return errors.New("learning JSON array is incomplete")
		}
	}
	return nil
}
