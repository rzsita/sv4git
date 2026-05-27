package sv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"
)

// MonorepoComponent is a versioned component discovered in a monorepo.
type MonorepoComponent struct {
	Name               string          // Directory name of the component
	RootPath           string          // Absolute path to the component root directory
	VersioningFilePath string          // Absolute path to the versioning file
	CurrentVersion     *semver.Version // Version read from the file
	VersionKeyMissing  bool            // True when the version key was not found in the file
}

// MonorepoProcessor discovers components and manages their file-based versions.
type MonorepoProcessor interface {
	FindComponents(repoRoot string, cfg MonorepoConfig) ([]MonorepoComponent, error)
	NextVersion(component MonorepoComponent, commits []GitCommitLog, semverProc SemVerCommitsProcessor) (*semver.Version, bool)
	UpdateVersion(component MonorepoComponent, version semver.Version, cfg MonorepoConfig) error
}

// MonorepoProcessorImpl is the default MonorepoProcessor.
type MonorepoProcessorImpl struct{}

// NewMonorepoProcessor MonorepoProcessorImpl constructor.
func NewMonorepoProcessor() *MonorepoProcessorImpl {
	return &MonorepoProcessorImpl{}
}

// FindComponents globs for versioning files and reads each component's current version.
// The glob pattern in cfg.VersioningFile is relative to repoRoot.
func (p MonorepoProcessorImpl) FindComponents(repoRoot string, cfg MonorepoConfig) ([]MonorepoComponent, error) {
	if cfg.VersioningFile == "" {
		return nil, fmt.Errorf("monorepo.versioning-file is not configured")
	}

	pattern := filepath.Join(repoRoot, cfg.VersioningFile)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid versioning-file glob %q: %v", cfg.VersioningFile, err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no files matched versioning-file pattern %q", cfg.VersioningFile)
	}

	components := make([]MonorepoComponent, 0, len(matches))
	for _, matchPath := range matches {
		keyMissing := false
		version, err := readVersionFromFile(matchPath, cfg.Path)
		if err != nil {
			// Distinguish "key not found" from structural errors (corrupt
			// YAML, permission denied, etc.). Only treat missing-key as a
			// new component; propagate other errors.
			if !isKeyNotFoundErr(matchPath, cfg.Path) {
				return nil, fmt.Errorf("reading version from %s: %v", matchPath, err)
			}
			version, _ = semver.NewVersion("0.0.0")
			keyMissing = true
		}
		dir := filepath.Dir(matchPath)
		components = append(components, MonorepoComponent{
			Name:               filepath.Base(dir),
			RootPath:           dir,
			VersioningFilePath: matchPath,
			CurrentVersion:     version,
			VersionKeyMissing:  keyMissing,
		})
	}
	return components, nil
}

// isKeyNotFoundErr returns true when the error from readVersionFromFile is
// caused by a missing key/path (as opposed to file-read errors, parse errors,
// or invalid semver at an existing key).
func isKeyNotFoundErr(filePath, dotPath string) bool {
	// If we can't even read or parse the file, it's not a missing-key error.
	content, err := os.ReadFile(filePath)
	if err != nil {
		return false
	}
	if _, err := parseFileContent(filePath, content); err != nil {
		return false
	}
	// The file parses fine — check whether the path resolves to a value.
	segments, err := parsePath(dotPath)
	if err != nil {
		return false
	}
	data, _ := parseFileContent(filePath, content)
	_, getErr := getByPath(data, segments)
	return getErr != nil && strings.Contains(getErr.Error(), "not found")
}

// NextVersion delegates to the existing SemVerCommitsProcessor.
func (p MonorepoProcessorImpl) NextVersion(component MonorepoComponent, commits []GitCommitLog, semverProc SemVerCommitsProcessor) (*semver.Version, bool) {
	return semverProc.NextVersion(component.CurrentVersion, commits)
}

// UpdateVersion writes the new version string into the component's versioning file.
// The version is formatted using cfg.VersionPrefix (derived from tag.pattern) so
// that a pattern like "v%d.%d.%d" produces "v1.2.3" in the file.
func (p MonorepoProcessorImpl) UpdateVersion(component MonorepoComponent, version semver.Version, cfg MonorepoConfig) error {
	versionStr := cfg.VersionPrefix + version.String()
	return writeVersionToFile(component.VersioningFilePath, cfg.Path, versionStr)
}

// ---- file I/O helpers ----

// ReadVersionFromBytes parses version from raw file content using the given dotPath.
// filePath is used only for format detection (YAML vs JSON) based on extension.
func ReadVersionFromBytes(filePath string, content []byte, dotPath string) (*semver.Version, error) {
	data, err := parseFileContent(filePath, content)
	if err != nil {
		return nil, err
	}
	segments, err := parsePath(dotPath)
	if err != nil {
		return nil, fmt.Errorf("invalid path %q: %v", dotPath, err)
	}
	raw, err := getByPath(data, segments)
	if err != nil {
		return nil, fmt.Errorf("path %q: %v", dotPath, err)
	}
	vstr, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("path %q: value is not a string", dotPath)
	}
	v, err := ToVersion(vstr)
	if err != nil {
		return nil, fmt.Errorf("path %q: invalid semver %q: %v", dotPath, vstr, err)
	}
	return v, nil
}

func readVersionFromFile(filePath, dotPath string) (*semver.Version, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	return ReadVersionFromBytes(filePath, content, dotPath)
}

// writeVersionToFile replaces the version value at dotPath in the file without
// altering any other formatting, comments, or key ordering. It locates the
// exact byte range of the version value and splices in the new string.
// If the key does not exist yet, it inserts the key-value pair at the
// appropriate location in the file.
func writeVersionToFile(filePath, dotPath, version string) error {
	// Preserve original file permissions.
	fi, statErr := os.Stat(filePath)
	perm := os.FileMode(0600)
	if statErr == nil {
		perm = fi.Mode().Perm()
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	segments, err := parsePath(dotPath)
	if err != nil {
		return fmt.Errorf("invalid path %q: %v", dotPath, err)
	}

	var start, end int
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".json":
		start, end, err = findJSONValueOffset(content, segments)
		if err != nil {
			// Key doesn't exist — insert it.
			content, err = insertJSONKey(content, segments, version)
			if err != nil {
				return fmt.Errorf("insert key at path %q: %v", dotPath, err)
			}
			return os.WriteFile(filePath, content, perm)
		}
	default:
		start, end, err = findYAMLValueOffset(content, segments)
		if err != nil {
			// Key doesn't exist — insert it.
			content, err = insertYAMLKey(content, segments, version)
			if err != nil {
				return fmt.Errorf("insert key at path %q: %v", dotPath, err)
			}
			return os.WriteFile(filePath, content, perm)
		}
	}

	result := make([]byte, 0, len(content)-end+start+len(version))
	result = append(result, content[:start]...)
	result = append(result, []byte(version)...)
	result = append(result, content[end:]...)

	return os.WriteFile(filePath, result, perm)
}

func parseFileContent(filePath string, content []byte) (map[string]interface{}, error) {
	var data map[string]interface{}
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".json":
		if err := json.Unmarshal(content, &data); err != nil {
			return nil, fmt.Errorf("parse JSON: %v", err)
		}
	default: // .yml, .yaml treated as YAML
		if err := yaml.Unmarshal(content, &data); err != nil {
			return nil, fmt.Errorf("parse YAML: %v", err)
		}
	}
	return data, nil
}

// ---- byte-offset finders ----

// findYAMLValueOffset locates the byte range [start, end) of the scalar value
// at the given path segments within YAML content. It preserves quoting style:
// for quoted scalars only the inner text range is returned.
func findYAMLValueOffset(content []byte, segments []string) (int, int, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return 0, 0, fmt.Errorf("parse YAML: %v", err)
	}

	// The top-level yaml.Node is a DocumentNode wrapping the actual content.
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return 0, 0, fmt.Errorf("unexpected YAML structure")
	}
	node := doc.Content[0]

	// Walk the tree following the path segments.
	for i, seg := range segments {
		if node.Kind != yaml.MappingNode {
			return 0, 0, fmt.Errorf("expected mapping at segment %q, got kind %d", seg, node.Kind)
		}
		found := false
		for j := 0; j < len(node.Content)-1; j += 2 {
			keyNode := node.Content[j]
			valNode := node.Content[j+1]
			if keyNode.Value == seg {
				if i == len(segments)-1 {
					// This is the leaf — compute byte offset.
					if valNode.Kind != yaml.ScalarNode {
						return 0, 0, fmt.Errorf("value at path is not a scalar (kind %d)", valNode.Kind)
					}
					return yamlScalarOffset(content, valNode)
				}
				node = valNode
				found = true
				break
			}
		}
		if !found {
			return 0, 0, fmt.Errorf("key %q not found", seg)
		}
	}

	return 0, 0, fmt.Errorf("path resolved to a non-leaf node")
}

// yamlScalarOffset computes the byte range of a scalar node's value text.
// For quoted strings (" or '), returns the range inside the quotes.
// For unquoted (plain) scalars, returns the range of the value text itself.
//
// Note: For double-quoted YAML scalars with escape sequences, node.Value
// contains the decoded text while the raw bytes contain the escapes. We scan
// for the closing quote in the raw bytes instead of relying on len(node.Value)
// to handle this correctly.
func yamlScalarOffset(content []byte, node *yaml.Node) (int, int, error) {
	offset := lineColumnToOffset(content, node.Line, node.Column)
	if offset < 0 || offset >= len(content) {
		return 0, 0, fmt.Errorf("computed offset %d out of range for content length %d", offset, len(content))
	}

	ch := content[offset]
	if ch == '"' || ch == '\'' {
		// Quoted scalar — scan for the matching closing quote in raw bytes.
		start := offset + 1
		end := start
		if ch == '"' {
			// Double-quoted: skip escaped characters.
			for end < len(content) {
				if content[end] == '\\' {
					end += 2 // skip escape sequence
					continue
				}
				if content[end] == '"' {
					break
				}
				end++
			}
		} else {
			// Single-quoted: '' is the escape for a literal '.
			for end < len(content) {
				if content[end] == '\'' {
					if end+1 < len(content) && content[end+1] == '\'' {
						end += 2 // escaped single quote
						continue
					}
					break
				}
				end++
			}
		}
		return start, end, nil
	}

	// Plain (unquoted) scalar.
	start := offset
	end := start + len(node.Value)
	return start, end, nil
}

// lineColumnToOffset converts 1-based line and column numbers (as reported
// by yaml.v3) to a 0-based byte offset into content.
func lineColumnToOffset(content []byte, line, col int) int {
	currentLine := 1
	for i := 0; i < len(content); i++ {
		if currentLine == line {
			return i + col - 1
		}
		if content[i] == '\n' {
			currentLine++
		}
	}
	// If line is beyond content, return -1.
	return -1
}

// findJSONValueOffset locates the byte range [start, end) of the string value
// (inside the quotes) at the given path segments within JSON content.
func findJSONValueOffset(content []byte, segments []string) (int, int, error) {
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.UseNumber()

	// We need to navigate into nested objects following the segments.
	// For each segment: enter the object, scan key tokens until we find the match,
	// then either descend (if more segments) or capture the value offset.
	for i, seg := range segments {
		// Expect '{' (start of object).
		t, err := dec.Token()
		if err != nil {
			return 0, 0, fmt.Errorf("expected object start: %v", err)
		}
		delim, ok := t.(json.Delim)
		if !ok || delim != '{' {
			return 0, 0, fmt.Errorf("expected '{', got %v", t)
		}

		found := false
		for dec.More() {
			// Read key.
			keyTok, err := dec.Token()
			if err != nil {
				return 0, 0, fmt.Errorf("reading key: %v", err)
			}
			key, ok := keyTok.(string)
			if !ok {
				return 0, 0, fmt.Errorf("expected string key, got %T", keyTok)
			}

			if key == seg {
				if i == len(segments)-1 {
					// This is the leaf key — the next token is our value.
					// Record offset before reading the value token.
					beforeOffset := dec.InputOffset()
					valTok, err := dec.Token()
					if err != nil {
						return 0, 0, fmt.Errorf("reading value: %v", err)
					}
					valStr, ok := valTok.(string)
					if !ok {
						return 0, 0, fmt.Errorf("value is not a string")
					}
					afterOffset := dec.InputOffset()
					// afterOffset points just past the closing '"'.
					// Find the value's byte range by scanning backwards and forwards.
					return jsonStringInnerRange(content, int(beforeOffset), int(afterOffset), valStr)
				}
				// More segments: don't consume the value, let the next
				// iteration's '{' read handle it.
				found = true
				break
			}

			// Skip the value for non-matching keys.
			if err := skipJSONValue(dec); err != nil {
				return 0, 0, fmt.Errorf("skipping value for key %q: %v", key, err)
			}
		}

		if !found {
			return 0, 0, fmt.Errorf("key %q not found", seg)
		}
	}

	return 0, 0, fmt.Errorf("path resolved to non-leaf")
}

// jsonStringInnerRange finds the byte range of a JSON string value's inner text
// (between the quotes) given the decoder's before/after offsets.
func jsonStringInnerRange(content []byte, beforeOffset, afterOffset int, value string) (int, int, error) {
	// afterOffset is right after the closing '"'. Walk backwards to find it.
	end := afterOffset - 1 // index of closing '"'
	if end < 0 || end >= len(content) || content[end] != '"' {
		return 0, 0, fmt.Errorf("cannot locate closing quote")
	}

	// Find the opening '"' for this string value by scanning forward from beforeOffset.
	start := -1
	for i := beforeOffset; i < end; i++ {
		if content[i] == '"' {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return 0, 0, fmt.Errorf("cannot locate opening quote")
	}

	// Validate that the content between quotes matches the expected value.
	if string(content[start:end]) != value {
		return 0, 0, fmt.Errorf("content mismatch: found %q, expected %q", string(content[start:end]), value)
	}

	return start, end, nil
}

// skipJSONValue advances the decoder past one complete JSON value (which may
// be an object, array, string, number, bool, or null).
func skipJSONValue(dec *json.Decoder) error {
	t, err := dec.Token()
	if err != nil {
		return err
	}
	_, ok := t.(json.Delim)
	if !ok {
		return nil // scalar — already consumed
	}
	// It's an object or array — skip until the matched closing delimiter.
	// We don't need to track which specific delimiter opened since
	// json.Decoder guarantees well-formed token sequences.
	depth := 1
	for depth > 0 {
		tt, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := tt.(json.Delim); ok {
			switch d {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
	}
	return nil
}

// ---- key insertion helpers ----

// yamlQuoteKey returns the key string, quoting it only when necessary to avoid
// YAML parsing ambiguity (e.g. when it contains ": " or " #").
func yamlQuoteKey(key string) string {
	if strings.Contains(key, ": ") || strings.Contains(key, " #") {
		return fmt.Sprintf("%q", key)
	}
	return key
}

// yamlMappingIndent returns the indentation string used by children of the
// given mapping node. If the mapping has children, the first key's column is
// used. Otherwise parentCol (the column of the key that points to this
// mapping) is used to compute an indent of parentCol-1 + indentStep spaces.
// indentStep defaults to 2 when it cannot be detected.
func yamlMappingIndent(node *yaml.Node, parentCol int) string {
	if len(node.Content) >= 2 {
		return strings.Repeat(" ", node.Content[0].Column-1)
	}
	// Empty or flow mapping — infer from parent.
	if parentCol > 0 {
		return strings.Repeat(" ", parentCol-1+2)
	}
	return ""
}

// yamlSpliceAfterMapping appends a new line after the last entry of a block
// mapping node. For empty/flow mappings (e.g. "key: {}") it converts them to
// block style by replacing the flow content with the new line.
func yamlSpliceAfterMapping(content []byte, node *yaml.Node, parentCol int, newLine string) []byte {
	if len(node.Content) > 0 {
		lastVal := node.Content[len(node.Content)-1]
		insertOffset := findEndOfNode(content, lastVal)
		result := make([]byte, 0, len(content)+len(newLine))
		result = append(result, content[:insertOffset]...)
		result = append(result, []byte(newLine)...)
		result = append(result, content[insertOffset:]...)
		return result
	}

	// Empty/flow mapping — find the "{}" or similar on the same line as the
	// mapping node and replace it with "\n" + newLine.
	nodeOffset := lineColumnToOffset(content, node.Line, node.Column)
	if nodeOffset < 0 {
		nodeOffset = 0
	}
	// For flow mappings the node itself starts at '{'. Find the matching '}'.
	if nodeOffset < len(content) && content[nodeOffset] == '{' {
		closeIdx := nodeOffset + 1
		for closeIdx < len(content) && content[closeIdx] != '}' {
			closeIdx++
		}
		if closeIdx < len(content) {
			closeIdx++ // past the '}'
			// Consume one optional trailing newline so we don't double up.
			if closeIdx < len(content) && content[closeIdx] == '\n' {
				closeIdx++
			}
			// Trim trailing whitespace before '{' (the " " in "key: {}").
			trimIdx := nodeOffset
			for trimIdx > 0 && (content[trimIdx-1] == ' ' || content[trimIdx-1] == '\t') {
				trimIdx--
			}
			result := make([]byte, 0, len(content)+len(newLine))
			result = append(result, content[:trimIdx]...)
			result = append(result, '\n')
			result = append(result, []byte(newLine)...)
			result = append(result, content[closeIdx:]...)
			return result
		}
	}

	// Fallback: insert at end of the mapping's line.
	endOffset := findEndOfNode(content, node)
	result := make([]byte, 0, len(content)+len(newLine))
	result = append(result, content[:endOffset]...)
	result = append(result, []byte(newLine)...)
	result = append(result, content[endOffset:]...)
	return result
}

// insertYAMLKey inserts a new key-value pair into YAML content at the location
// described by segments. Missing intermediate parent mappings are created
// automatically. The new entries use indentation inferred from existing
// siblings or the parent mapping's indent level + 2 spaces.
func insertYAMLKey(content []byte, segments []string, value string) ([]byte, error) {
	if len(segments) == 0 {
		return nil, fmt.Errorf("empty path segments")
	}

	leafKey := segments[len(segments)-1]
	parentSegments := segments[:len(segments)-1]

	// --- Phase 1: ensure all intermediate parent mappings exist ---
	for depth := 0; depth < len(parentSegments); depth++ {
		var doc yaml.Node
		if err := yaml.Unmarshal(content, &doc); err != nil {
			return nil, fmt.Errorf("parse YAML: %v", err)
		}
		if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
			return nil, fmt.Errorf("unexpected YAML structure")
		}

		node := doc.Content[0]
		parentCol := 0 // column of the key that led to `node`

		// Walk segments[0..depth] — all of these should exist.
		missing := false
		for i := 0; i <= depth; i++ {
			seg := parentSegments[i]
			if node.Kind != yaml.MappingNode {
				return nil, fmt.Errorf("expected mapping at segment %q, got kind %d", seg, node.Kind)
			}
			found := false
			for j := 0; j < len(node.Content)-1; j += 2 {
				if node.Content[j].Value == seg {
					parentCol = node.Content[j].Column
					node = node.Content[j+1]
					found = true
					break
				}
			}
			if !found {
				if i < depth {
					// An earlier segment that we already created is missing —
					// this shouldn't happen since we create in order.
					return nil, fmt.Errorf("parent key %q not found", seg)
				}
				// segments[depth] is missing — create it as an empty
				// flow mapping so it parses as a MappingNode (not a
				// null scalar, which "key:\n" would produce).
				indent := yamlMappingIndent(node, parentCol)
				newLine := fmt.Sprintf("%s%s: {}\n", indent, yamlQuoteKey(seg))
				content = yamlSpliceAfterMapping(content, node, parentCol, newLine)
				missing = true
				break
			}
		}
		if missing {
			continue // re-parse and verify on next iteration
		}
		// All segments up to depth exist; nothing to create at this depth.
	}

	// --- Phase 2: insert the leaf key ---
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("parse YAML: %v", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, fmt.Errorf("unexpected YAML structure")
	}

	node := doc.Content[0]
	parentCol := 0
	for _, seg := range parentSegments {
		if node.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("expected mapping at segment %q, got kind %d", seg, node.Kind)
		}
		found := false
		for j := 0; j < len(node.Content)-1; j += 2 {
			if node.Content[j].Value == seg {
				parentCol = node.Content[j].Column
				node = node.Content[j+1]
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("parent key %q not found after creation", seg)
		}
	}

	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("parent is not a mapping (kind %d)", node.Kind)
	}

	indent := yamlMappingIndent(node, parentCol)
	newLine := fmt.Sprintf("%s%s: %s\n", indent, yamlQuoteKey(leafKey), value)

	return yamlSpliceAfterMapping(content, node, parentCol, newLine), nil
}

// findEndOfNode returns the byte offset just past a YAML node's content
// (i.e., after the newline at end of the node's line, or the end of file).
// For container nodes (sequences, mappings) with children, it recurses to
// find the end of the last child so that multi-line structures are fully
// covered.
func findEndOfNode(content []byte, node *yaml.Node) int {
	// For container nodes with children, the end is past the last child.
	if (node.Kind == yaml.SequenceNode || node.Kind == yaml.MappingNode) && len(node.Content) > 0 {
		return findEndOfNode(content, node.Content[len(node.Content)-1])
	}

	offset := lineColumnToOffset(content, node.Line, node.Column)
	if offset < 0 {
		return len(content)
	}
	// Advance to end of line.
	for offset < len(content) && content[offset] != '\n' {
		offset++
	}
	// Include the newline itself.
	if offset < len(content) {
		offset++
	}
	return offset
}

// insertJSONKey inserts a new key-value pair into JSON content at the location
// described by segments. Missing intermediate parent objects are created
// automatically. It detects the existing indentation style and formats new
// entries to match.
func insertJSONKey(content []byte, segments []string, value string) ([]byte, error) {
	if len(segments) == 0 {
		return nil, fmt.Errorf("empty path segments")
	}

	leafKey := segments[len(segments)-1]
	parentSegments := segments[:len(segments)-1]

	// --- Phase 1: ensure all intermediate parent objects exist ---
	for depth := 0; depth < len(parentSegments); depth++ {
		missing, insertContent, err := jsonEnsureParent(content, parentSegments, depth)
		if err != nil {
			return nil, err
		}
		if missing {
			content = insertContent
		}
	}

	// --- Phase 2: insert the leaf key ---
	return jsonInsertLeaf(content, parentSegments, leafKey, value)
}

// jsonEnsureParent checks whether parentSegments[depth] exists. If not, it
// inserts an empty object for that key and returns (true, modifiedContent, nil).
// If the key already exists it returns (false, nil, nil).
func jsonEnsureParent(content []byte, parentSegments []string, depth int) (bool, []byte, error) {
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.UseNumber()

	// Navigate to segments[0..depth-1] — these must all exist.
	for i := 0; i < depth; i++ {
		if err := jsonEnterObject(dec); err != nil {
			return false, nil, err
		}
		if err := jsonSeekKey(dec, parentSegments[i]); err != nil {
			return false, nil, fmt.Errorf("parent key %q not found", parentSegments[i])
		}
	}

	// Enter the object at segments[depth-1] (or root if depth == 0).
	if err := jsonEnterObject(dec); err != nil {
		return false, nil, err
	}

	// Check if segments[depth] exists.
	seg := parentSegments[depth]
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return false, nil, err
		}
		key, _ := keyTok.(string)
		if key == seg {
			return false, nil, nil // already exists
		}
		if err := skipJSONValue(dec); err != nil {
			return false, nil, err
		}
	}

	// Missing — insert `"seg": {}` into this object.
	// Re-scan to determine whether this object already has entries.
	dec2 := json.NewDecoder(bytes.NewReader(content))
	dec2.UseNumber()
	for i := 0; i < depth; i++ {
		_ = jsonEnterObject(dec2)
		_ = jsonSeekKey(dec2, parentSegments[i])
	}
	_ = jsonEnterObject(dec2)
	hasEntries := dec2.More()
	// Skip to end.
	for dec2.More() {
		if _, err := dec2.Token(); err != nil {
			return false, nil, err
		}
		if err := skipJSONValue(dec2); err != nil {
			return false, nil, err
		}
	}
	if _, err := dec2.Token(); err != nil {
		return false, nil, err
	}
	closingOffset := int(dec2.InputOffset()) - 1

	indent := detectJSONIndent(content, closingOffset)
	var newEntry string
	var insertAt int
	if indent != "" {
		closingIndent := ""
		nlIdx := lastNewlineBefore(content, closingOffset)
		if nlIdx >= 0 {
			closingIndent = string(content[nlIdx+1 : closingOffset])
		}
		keyIndent := closingIndent + indent
		if hasEntries {
			insertAt = nlIdx
			newEntry = fmt.Sprintf(",\n%s%q: {}", keyIndent, seg)
		} else {
			insertAt = closingOffset
			newEntry = fmt.Sprintf("\n%s%q: {}\n%s", keyIndent, seg, closingIndent)
		}
	} else {
		insertAt = closingOffset
		newEntry = fmt.Sprintf("%q: {}", seg)
		if hasEntries {
			newEntry = ", " + newEntry
		}
	}

	result := make([]byte, 0, len(content)+len(newEntry))
	result = append(result, content[:insertAt]...)
	result = append(result, []byte(newEntry)...)
	result = append(result, content[insertAt:]...)
	return true, result, nil
}

// jsonInsertLeaf inserts leafKey: value into the object reached by navigating
// parentSegments. All parents must already exist.
func jsonInsertLeaf(content []byte, parentSegments []string, leafKey, value string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.UseNumber()

	for _, seg := range parentSegments {
		if err := jsonEnterObject(dec); err != nil {
			return nil, err
		}
		if err := jsonSeekKey(dec, seg); err != nil {
			return nil, fmt.Errorf("parent key %q not found", seg)
		}
	}

	if err := jsonEnterObject(dec); err != nil {
		return nil, err
	}

	hasEntries := false
	for dec.More() {
		hasEntries = true
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		if err := skipJSONValue(dec); err != nil {
			return nil, err
		}
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	closingOffset := int(dec.InputOffset()) - 1

	indent := detectJSONIndent(content, closingOffset)

	var newEntry string
	var insertAt int
	if indent != "" {
		closingIndent := ""
		nlIdx := lastNewlineBefore(content, closingOffset)
		if nlIdx >= 0 {
			closingIndent = string(content[nlIdx+1 : closingOffset])
		}
		keyIndent := closingIndent + indent
		if hasEntries {
			insertAt = nlIdx
			newEntry = fmt.Sprintf(",\n%s%q: %q", keyIndent, leafKey, value)
		} else {
			insertAt = closingOffset
			newEntry = fmt.Sprintf("\n%s%q: %q\n%s", keyIndent, leafKey, value, closingIndent)
		}
	} else {
		insertAt = closingOffset
		newEntry = fmt.Sprintf("%q: %q", leafKey, value)
		if hasEntries {
			newEntry = ", " + newEntry
		}
	}

	result := make([]byte, 0, len(content)+len(newEntry))
	result = append(result, content[:insertAt]...)
	result = append(result, []byte(newEntry)...)
	result = append(result, content[insertAt:]...)
	return result, nil
}

// jsonEnterObject expects and consumes a '{' token from the decoder.
func jsonEnterObject(dec *json.Decoder) error {
	t, err := dec.Token()
	if err != nil {
		return fmt.Errorf("expected object start: %v", err)
	}
	d, ok := t.(json.Delim)
	if !ok || d != '{' {
		return fmt.Errorf("expected '{', got %v", t)
	}
	return nil
}

// jsonSeekKey scans the current object's keys until it finds the given key,
// leaving the decoder positioned to read that key's value. Returns an error
// if the key is not found.
func jsonSeekKey(dec *json.Decoder, key string) error {
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		k, _ := keyTok.(string)
		if k == key {
			return nil
		}
		if err := skipJSONValue(dec); err != nil {
			return err
		}
	}
	return fmt.Errorf("key %q not found", key)
}

// detectJSONIndent examines the content before closingOffset to determine the
// indentation unit used (e.g. "  " or "    " or "\t"). Returns "" for compact JSON.
func detectJSONIndent(content []byte, closingOffset int) string {
	// Find the newline before the closing '}'.
	nlIdx := lastNewlineBefore(content, closingOffset)
	if nlIdx < 0 {
		return "" // no newline → compact JSON
	}
	closingIndent := string(content[nlIdx+1 : closingOffset])
	if strings.TrimSpace(closingIndent) != "" {
		return "" // non-whitespace before '}' → not standard pretty-print
	}

	// Find the previous newline to get a key line's indentation.
	for i := nlIdx - 1; i >= 0; i-- {
		if content[i] == '\n' {
			lineContent := string(content[i+1 : nlIdx])
			trimmed := strings.TrimLeft(lineContent, " \t")
			keyIndent := lineContent[:len(lineContent)-len(trimmed)]
			if len(keyIndent) > len(closingIndent) {
				return keyIndent[len(closingIndent):]
			}
			break
		}
	}

	// If closingIndent is pure whitespace, assume a default indent unit.
	if len(closingIndent) > 0 {
		return "  "
	}
	return ""
}

// lastNewlineBefore finds the last '\n' before pos in content. Returns -1 if none.
func lastNewlineBefore(content []byte, pos int) int {
	for i := pos - 1; i >= 0; i-- {
		if content[i] == '\n' {
			return i
		}
	}
	return -1
}

// ---- path parsing and navigation ----

// parsePath parses a jq/yq-style path expression into key segments.
//
// Supported formats:
//
//	metadata.version                                  → ["metadata", "version"]
//	.metadata.version                                 → ["metadata", "version"]  (leading dot optional)
//	.metadata.annotations["backstage.io/my-key"]     → ["metadata", "annotations", "backstage.io/my-key"]
//	metadata["key.with.dots"].nested                  → ["metadata", "key.with.dots", "nested"]
//
// Inside bracket notation ["..."] or ['...'] the content is treated as a literal
// key name, allowing dots and other special characters.
func parsePath(path string) ([]string, error) {
	if path == "" {
		return nil, fmt.Errorf("empty path")
	}

	var segments []string
	var current strings.Builder
	i := 0

	// Strip optional leading dot (jq style).
	if path[0] == '.' {
		i = 1
	}

	for i < len(path) {
		switch path[i] {
		case '.':
			if current.Len() > 0 {
				segments = append(segments, current.String())
				current.Reset()
			}
			i++

		case '[':
			if current.Len() > 0 {
				segments = append(segments, current.String())
				current.Reset()
			}
			i++ // skip '['
			if i >= len(path) {
				return nil, fmt.Errorf("unexpected end of path after '['")
			}
			quote := path[i]
			if quote != '"' && quote != '\'' {
				return nil, fmt.Errorf("expected quote character after '[', got %q", string(quote))
			}
			i++ // skip opening quote
			for i < len(path) && path[i] != quote {
				current.WriteByte(path[i])
				i++
			}
			if i >= len(path) {
				return nil, fmt.Errorf("unclosed string in bracket notation")
			}
			i++ // skip closing quote
			if i >= len(path) || path[i] != ']' {
				return nil, fmt.Errorf("expected ']' to close bracket notation")
			}
			i++ // skip ']'
			segments = append(segments, current.String())
			current.Reset()
			// Skip optional trailing dot after ']'.
			if i < len(path) && path[i] == '.' {
				i++
			}

		default:
			current.WriteByte(path[i])
			i++
		}
	}

	if current.Len() > 0 {
		segments = append(segments, current.String())
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("path %q contains no segments", path)
	}
	return segments, nil
}

// getByPath navigates a nested map[string]interface{} using pre-parsed key segments.
func getByPath(data map[string]interface{}, segments []string) (interface{}, error) {
	if len(segments) == 0 {
		return nil, fmt.Errorf("empty path")
	}
	val, ok := data[segments[0]]
	if !ok {
		return nil, fmt.Errorf("key %q not found", segments[0])
	}
	if len(segments) == 1 {
		return val, nil
	}
	nested, ok := val.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("value at %q is not a map", segments[0])
	}
	return getByPath(nested, segments[1:])
}

// setByPath sets a value in a nested map[string]interface{} using dot-separated segments.
func setByPath(data map[string]interface{}, segments []string, value string) error {
	if len(segments) == 0 {
		return fmt.Errorf("empty path")
	}
	if len(segments) == 1 {
		if _, ok := data[segments[0]]; !ok {
			return fmt.Errorf("key %q not found", segments[0])
		}
		data[segments[0]] = value
		return nil
	}
	val, ok := data[segments[0]]
	if !ok {
		return fmt.Errorf("key %q not found", segments[0])
	}
	nested, ok := val.(map[string]interface{})
	if !ok {
		return fmt.Errorf("value at %q is not a map", segments[0])
	}
	return setByPath(nested, segments[1:], value)
}
