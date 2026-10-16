package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// A review can make several model requests, each with its own timeout.
const codexToolTimeoutSeconds = 3600

type tomlEdit struct {
	start, end int
	text       string
}

// mergeTOML preserves user settings and comments outside the managed values.
func mergeTOML(path, cmd string, args []string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	cfg := map[string]any{}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	servers, err := ensureObject(cfg, "mcp_servers")
	if err != nil {
		return nil, err
	}
	_, existed := servers["nitpick"]
	server, err := ensureObject(servers, "nitpick")
	if err != nil {
		return nil, err
	}
	pending := map[string]bool{}
	if server["command"] != cmd {
		pending["command"] = true
	}
	if !reflect.DeepEqual(server["args"], toAny(args)) {
		pending["args"] = true
	}
	server["command"], server["args"] = cmd, toAny(args)
	if _, ok := server["tool_timeout_sec"]; !ok {
		server["tool_timeout_sec"] = codexToolTimeoutSeconds
		pending["tool_timeout_sec"] = true
	}
	return editCodexTOML(data, cfg, pending, !existed)
}

func editCodexTOML(data []byte, cfg map[string]any, pending map[string]bool, newServer bool) ([]byte, error) {
	target := []string{"mcp_servers", "nitpick"}
	var edits []tomlEdit
	var scope, insertScope []string
	insertAt := 0
	var parser unstable.Parser
	parser.Reset(data)
	for parser.NextExpression() {
		node := parser.Expression()
		switch node.Kind {
		case unstable.Table, unstable.ArrayTable:
			scope = tomlKey(node)
			if node.Kind == unstable.Table && pathPrefix(scope, target) && len(scope) > len(insertScope) {
				insertScope = slices.Clone(scope)
				// Insert after the whole header, including any trailing comment.
				key := node.Key()
				key.Next()
				start := int(key.Node().Raw.Offset)
				end := bytes.IndexByte(data[start:], '\n')
				insertAt = len(data)
				if end >= 0 {
					insertAt = start + end + 1
				}
			}
		case unstable.KeyValue:
			key := tomlKey(node)
			full := append(slices.Clone(scope), key...)
			managed := false
			for field := range pending {
				if pathPrefix(full, append(slices.Clone(target), field)) {
					delete(pending, field)
					managed = true
				}
			}
			if !managed {
				continue
			}
			value := any(cfg)
			for _, part := range full {
				value = value.(map[string]any)[part]
			}
			encoded, err := encodeTOMLValue(value)
			if err != nil {
				return nil, err
			}
			// KeyValue.Raw spans complete arrays and multiline strings; Value.Raw
			// does not span array values in the parser's AST.
			keys := node.Key()
			var last *unstable.Node
			for keys.Next() {
				last = keys.Node()
			}
			start := int(last.Raw.Offset + last.Raw.Length)
			start += bytes.IndexByte(data[start:], '=') + 1
			for data[start] == ' ' || data[start] == '\t' {
				start++
			}
			end := int(node.Raw.Offset + node.Raw.Length)
			edits = append(edits, tomlEdit{start: start, end: end, text: encoded})
		}
	}
	if err := parser.Error(); err != nil {
		return nil, fmt.Errorf("parse MCP settings: %w", err)
	}
	var additions strings.Builder
	if newServer && len(pending) == 3 {
		insertAt = len(data)
		insertScope = target
		additions.WriteString("\n[mcp_servers.nitpick]\n")
	}
	server := cfg["mcp_servers"].(map[string]any)["nitpick"].(map[string]any)
	for _, field := range []string{"command", "args", "tool_timeout_sec"} {
		if !pending[field] {
			continue
		}
		value, err := encodeTOMLValue(server[field])
		if err != nil {
			return nil, err
		}
		key := append(slices.Clone(target[len(insertScope):]), field)
		fmt.Fprintf(&additions, "%s = %s\n", strings.Join(key, "."), value)
	}
	if additions.Len() > 0 {
		prefix := ""
		if insertAt > 0 && data[insertAt-1] != '\n' {
			prefix = "\n"
		}
		edits = append(edits, tomlEdit{start: insertAt, end: insertAt, text: prefix + additions.String()})
	}
	slices.SortFunc(edits, func(a, b tomlEdit) int { return b.start - a.start })
	out := slices.Clone(data)
	for _, edit := range edits {
		out = append(out[:edit.start], append([]byte(edit.text), out[edit.end:]...)...)
	}
	// Validate the combined document before installation can write it.
	var check map[string]any
	if err := toml.Unmarshal(out, &check); err != nil {
		return nil, fmt.Errorf("updated MCP config is invalid: %w", err)
	}
	return out, nil
}

func tomlKey(node *unstable.Node) []string {
	var key []string
	parts := node.Key()
	for parts.Next() {
		key = append(key, string(parts.Node().Data))
	}
	return key
}

func pathPrefix(prefix, path []string) bool {
	return len(prefix) <= len(path) && slices.Equal(prefix, path[:len(prefix)])
}

func encodeTOMLValue(value any) (string, error) {
	var out bytes.Buffer
	err := toml.NewEncoder(&out).SetTablesInline(true).Encode(map[string]any{"value": value})
	if err != nil {
		return "", fmt.Errorf("encode MCP setting: %w", err)
	}
	return strings.TrimSpace(strings.TrimPrefix(out.String(), "value = ")), nil
}

func ensureObject(cfg map[string]any, key string) (map[string]any, error) {
	if value, ok := cfg[key]; ok {
		if object, ok := value.(map[string]any); ok {
			return object, nil
		}
		return nil, fmt.Errorf("%s must be a table", key)
	}
	object := map[string]any{}
	cfg[key] = object
	return object, nil
}
