package sv

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// ---- parsePath tests ----

func TestParsePath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		path    string
		want    []string
		wantErr bool
	}{
		{
			name: "simple key",
			path: "version",
			want: []string{"version"},
		},
		{
			name: "nested dot notation",
			path: "metadata.version",
			want: []string{"metadata", "version"},
		},
		{
			name: "leading dot (jq style)",
			path: ".metadata.version",
			want: []string{"metadata", "version"},
		},
		{
			name: "bracket notation double quotes",
			path: `metadata["key.with.dots"]`,
			want: []string{"metadata", "key.with.dots"},
		},
		{
			name: "bracket notation single quotes",
			path: `metadata['key.with.dots']`,
			want: []string{"metadata", "key.with.dots"},
		},
		{
			name: "backstage jq-style path",
			path: `.metadata.annotations["backstage.io/template-version"]`,
			want: []string{"metadata", "annotations", "backstage.io/template-version"},
		},
		{
			name: "bracket followed by dot then field",
			path: `metadata["section"].version`,
			want: []string{"metadata", "section", "version"},
		},
		{
			name: "top-level bracket notation without leading dot",
			path: `["top-level-key"]`,
			want: []string{"top-level-key"},
		},
		{
			name:    "empty path",
			path:    "",
			wantErr: true,
		},
		{
			name:    "unclosed bracket",
			path:    `metadata["key`,
			wantErr: true,
		},
		{
			name:    "missing quote after bracket",
			path:    "metadata[key]",
			wantErr: true,
		},
		{
			name:    "missing closing bracket",
			path:    `metadata["key"`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parsePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("parsePath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("parsePath() = %v, want %v", got, tt.want)
				return
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("parsePath() segment[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// ---- getByPath tests ----

func TestGetByPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		data     map[string]interface{}
		segments []string
		want     interface{}
		wantErr  bool
	}{
		{
			name:     "simple key",
			data:     map[string]interface{}{"version": "1.2.3"},
			segments: []string{"version"},
			want:     "1.2.3",
		},
		{
			name: "nested key",
			data: map[string]interface{}{
				"metadata": map[string]interface{}{"version": "2.0.0"},
			},
			segments: []string{"metadata", "version"},
			want:     "2.0.0",
		},
		{
			name:     "empty segments",
			data:     map[string]interface{}{},
			segments: []string{},
			wantErr:  true,
		},
		{
			name:     "missing key",
			data:     map[string]interface{}{"other": "value"},
			segments: []string{"version"},
			wantErr:  true,
		},
		{
			name: "intermediate value is not a map",
			data: map[string]interface{}{
				"metadata": "not-a-map",
			},
			segments: []string{"metadata", "version"},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := getByPath(tt.data, tt.segments)
			if (err != nil) != tt.wantErr {
				t.Errorf("getByPath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("getByPath() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---- setByPath tests ----

func TestSetByPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		data     map[string]interface{}
		segments []string
		value    string
		wantErr  bool
		wantVal  string
	}{
		{
			name:     "simple key",
			data:     map[string]interface{}{"version": "1.0.0"},
			segments: []string{"version"},
			value:    "2.0.0",
			wantVal:  "2.0.0",
		},
		{
			name: "nested key",
			data: map[string]interface{}{
				"metadata": map[string]interface{}{"version": "1.0.0"},
			},
			segments: []string{"metadata", "version"},
			value:    "1.1.0",
			wantVal:  "1.1.0",
		},
		{
			name:     "empty segments",
			data:     map[string]interface{}{},
			segments: []string{},
			wantErr:  true,
		},
		{
			name:     "missing key",
			data:     map[string]interface{}{},
			segments: []string{"missing"},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := setByPath(tt.data, tt.segments, tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("setByPath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			got, ferr := getByPath(tt.data, tt.segments)
			if ferr != nil {
				t.Fatalf("getByPath() after set failed: %v", ferr)
			}
			if got != tt.wantVal {
				t.Errorf("after setByPath() value = %v, want %v", got, tt.wantVal)
			}
		})
	}
}

// ---- readVersionFromFile tests ----

func TestReadVersionFromFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		content  string
		ext      string
		dotPath  string
		want     string
		wantErr  bool
	}{
		{
			name:    "simple yaml",
			ext:     ".yml",
			content: "version: 1.2.3\n",
			dotPath: "version",
			want:    "1.2.3",
		},
		{
			name:    "nested yaml",
			ext:     ".yaml",
			content: "metadata:\n  version: 2.0.0\n",
			dotPath: "metadata.version",
			want:    "2.0.0",
		},
		{
			name:    "backstage yaml with bracket notation",
			ext:     ".yml",
			content: "metadata:\n  annotations:\n    backstage.io/template-version: 3.1.4\n",
			dotPath: `.metadata.annotations["backstage.io/template-version"]`,
			want:    "3.1.4",
		},
		{
			name:    "simple json",
			ext:     ".json",
			content: `{"version": "1.0.0"}`,
			dotPath: "version",
			want:    "1.0.0",
		},
		{
			name:    "nested json",
			ext:     ".json",
			content: `{"metadata": {"version": "0.5.0"}}`,
			dotPath: "metadata.version",
			want:    "0.5.0",
		},
		{
			name:    "missing path",
			ext:     ".yml",
			content: "other: value\n",
			dotPath: "version",
			wantErr: true,
		},
		{
			name:    "invalid semver",
			ext:     ".yml",
			content: "version: not-a-version\n",
			dotPath: "version",
			wantErr: true,
		},
		{
			name:    "value is not string",
			ext:     ".yml",
			content: "version: 123\n",
			dotPath: "version",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, err := os.CreateTemp(t.TempDir(), "versionfile*"+tt.ext)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = f.Close() })
			if _, err := f.WriteString(tt.content); err != nil {
				t.Fatal(err)
			}
			if err := f.Sync(); err != nil {
				t.Fatal(err)
			}

			got, err := readVersionFromFile(f.Name(), tt.dotPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("readVersionFromFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got.Original() != tt.want {
				t.Errorf("readVersionFromFile() = %v, want %v", got.Original(), tt.want)
			}
		})
	}
}

// ---- writeVersionToFile tests ----

func TestWriteVersionToFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		ext         string
		content     string
		dotPath     string
		version     string
		wantErr     bool
		wantContent string // if non-empty, exact file bytes expected after write
	}{
		{
			name:        "yaml round-trip",
			ext:         ".yml",
			content:     "version: 1.0.0\n",
			dotPath:     "version",
			version:     "1.1.0",
			wantContent: "version: 1.1.0\n",
		},
		{
			name:        "json round-trip",
			ext:         ".json",
			content:     `{"version": "1.0.0"}`,
			dotPath:     "version",
			version:     "2.0.0",
			wantContent: `{"version": "2.0.0"}`,
		},
		{
			name:        "nested yaml round-trip",
			ext:         ".yaml",
			content:     "metadata:\n  version: 0.1.0\n",
			dotPath:     "metadata.version",
			version:     "0.2.0",
			wantContent: "metadata:\n  version: 0.2.0\n",
		},
		{
			name:        "yaml inserts missing leaf key",
			ext:         ".yml",
			content:     "other: value\n",
			dotPath:     "other",
			version:     "1.0.0",
			wantContent: "other: 1.0.0\n",
		},
		{
			name:        "yaml inserts missing nested key",
			ext:         ".yml",
			content:     "metadata:\n  annotations:\n    backstage.io/techdocs-ref: dir:.\n",
			dotPath:     `.metadata.annotations["backstage.io/template-version"]`,
			version:     "1.0.0",
			wantContent: "metadata:\n  annotations:\n    backstage.io/techdocs-ref: dir:.\n    backstage.io/template-version: 1.0.0\n",
		},
		{
			name:        "json inserts missing key",
			ext:         ".json",
			content:     `{"metadata": {"name": "test"}}`,
			dotPath:     "metadata.version",
			version:     "1.0.0",
			wantContent: `{"metadata": {"name": "test", "version": "1.0.0"}}`,
		},
		{
			name:        "yaml preserves comments",
			ext:         ".yml",
			content:     "# Component config\nmetadata:\n  name: my-svc\n  version: 1.0.0  # current ver\nother: stuff\n",
			dotPath:     "metadata.version",
			version:     "1.1.0",
			wantContent: "# Component config\nmetadata:\n  name: my-svc\n  version: 1.1.0  # current ver\nother: stuff\n",
		},
		{
			name:        "yaml preserves double-quoted value",
			ext:         ".yml",
			content:     "version: \"1.0.0\"\n",
			dotPath:     "version",
			version:     "2.0.0",
			wantContent: "version: \"2.0.0\"\n",
		},
		{
			name:        "yaml preserves single-quoted value",
			ext:         ".yml",
			content:     "version: '1.0.0'\n",
			dotPath:     "version",
			version:     "2.0.0",
			wantContent: "version: '2.0.0'\n",
		},
		{
			name: "yaml preserves blank lines and indentation",
			ext:  ".yml",
			content: `# Top comment

metadata:
    version: 3.0.0

    name: test
`,
			dotPath: "metadata.version",
			version: "3.1.0",
			wantContent: `# Top comment

metadata:
    version: 3.1.0

    name: test
`,
		},
		{
			name:        "yaml bracket notation path preserves formatting",
			ext:         ".yml",
			content:     "metadata:\n  annotations:\n    backstage.io/template-version: 1.2.3\n",
			dotPath:     `.metadata.annotations["backstage.io/template-version"]`,
			version:     "1.3.0",
			wantContent: "metadata:\n  annotations:\n    backstage.io/template-version: 1.3.0\n",
		},
		{
			name: "json preserves custom indentation",
			ext:  ".json",
			content: `{
    "name": "test",
    "metadata": {
        "version": "1.0.0"
    }
}`,
			dotPath: "metadata.version",
			version: "1.1.0",
			wantContent: `{
    "name": "test",
    "metadata": {
        "version": "1.1.0"
    }
}`,
		},
		{
			name:        "json does not touch other version-like strings",
			ext:         ".json",
			content:     `{"name":"1.0.0","version":"1.0.0","description":"ver 1.0.0"}`,
			dotPath:     "version",
			version:     "2.0.0",
			wantContent: `{"name":"1.0.0","version":"2.0.0","description":"ver 1.0.0"}`,
		},
		{
			name:        "yaml version length change (shorter)",
			ext:         ".yml",
			content:     "version: 10.20.30\nname: test\n",
			dotPath:     "version",
			version:     "1.0.0",
			wantContent: "version: 1.0.0\nname: test\n",
		},
		{
			name:        "yaml version length change (longer)",
			ext:         ".yml",
			content:     "version: 1.0.0\nname: test\n",
			dotPath:     "version",
			version:     "10.20.30",
			wantContent: "version: 10.20.30\nname: test\n",
		},
		{
			name: "json inserts key into pretty-printed object",
			ext:  ".json",
			content: `{
    "name": "test",
    "metadata": {
        "name": "inner"
    }
}`,
			dotPath: "metadata.version",
			version: "1.0.0",
			wantContent: `{
    "name": "test",
    "metadata": {
        "name": "inner",
        "version": "1.0.0"
    }
}`,
		},
		{
			name:        "yaml inserts missing intermediate parent",
			ext:         ".yml",
			content:     "metadata:\n  name: test\n",
			dotPath:     `.metadata.annotations["backstage.io/template-version"]`,
			version:     "1.0.0",
			wantContent: "metadata:\n  name: test\n  annotations:\n    backstage.io/template-version: 1.0.0\n",
		},
		{
			name:        "yaml inserts intermediate parent after sequence sibling",
			ext:         ".yml",
			content:     "metadata:\n  name: test\n  tags:\n    - lz\n    - azure\nspec:\n  owner: team\n",
			dotPath:     `.metadata.annotations["backstage.io/template-version"]`,
			version:     "0.1.0",
			wantContent: "metadata:\n  name: test\n  tags:\n    - lz\n    - azure\n  annotations:\n    backstage.io/template-version: 0.1.0\nspec:\n  owner: team\n",
		},
		{
			name:        "yaml inserts multiple missing intermediate parents",
			ext:         ".yml",
			content:     "metadata:\n  name: test\n",
			dotPath:     "metadata.annotations.deep.version",
			version:     "2.0.0",
			wantContent: "metadata:\n  name: test\n  annotations:\n    deep:\n      version: 2.0.0\n",
		},
		{
			name:        "yaml inserts into empty flow mapping",
			ext:         ".yml",
			content:     "metadata: {}\n",
			dotPath:     "metadata.version",
			version:     "1.0.0",
			wantContent: "metadata:\n  version: 1.0.0\n",
		},
		{
			name:        "yaml inserts missing parent at top level",
			ext:         ".yml",
			content:     "name: test\n",
			dotPath:     "metadata.version",
			version:     "1.0.0",
			wantContent: "name: test\nmetadata:\n  version: 1.0.0\n",
		},
		{
			name:        "json inserts missing intermediate parent",
			ext:         ".json",
			content:     `{"metadata": {"name": "test"}}`,
			dotPath:     `metadata.annotations.version`,
			version:     "1.0.0",
			wantContent: `{"metadata": {"name": "test", "annotations": {"version": "1.0.0"}}}`,
		},
		{
			name:        "yaml with document separator",
			ext:         ".yml",
			content:     "---\nversion: 1.0.0\nname: test\n",
			dotPath:     "version",
			version:     "2.0.0",
			wantContent: "---\nversion: 2.0.0\nname: test\n",
		},
		{
			name:        "yaml version with v-prefix round-trip",
			ext:         ".yml",
			content:     "version: v1.0.0\n",
			dotPath:     "version",
			version:     "v2.0.0",
			wantContent: "version: v2.0.0\n",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			fpath := filepath.Join(dir, "versionfile"+tt.ext)
			if err := os.WriteFile(fpath, []byte(tt.content), 0600); err != nil {
				t.Fatal(err)
			}

			err := writeVersionToFile(fpath, tt.dotPath, tt.version)
			if (err != nil) != tt.wantErr {
				t.Errorf("writeVersionToFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			got, rerr := readVersionFromFile(fpath, tt.dotPath)
			if rerr != nil {
				t.Fatalf("readVersionFromFile() after write failed: %v", rerr)
			}
			if got.Original() != tt.version {
				t.Errorf("after write, version = %v, want %v", got.Original(), tt.version)
			}

			if tt.wantContent != "" {
				written, err := os.ReadFile(fpath)
				if err != nil {
					t.Fatalf("reading file after write: %v", err)
				}
				if string(written) != tt.wantContent {
					t.Errorf("file content mismatch.\ngot:\n%s\nwant:\n%s", string(written), tt.wantContent)
				}
			}
		})
	}
}

// ---- findYAMLValueOffset tests ----

func TestFindYAMLValueOffset(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		content  string
		segments []string
		wantStr  string
		wantErr  bool
	}{
		{
			name:     "simple unquoted",
			content:  "version: 1.0.0\n",
			segments: []string{"version"},
			wantStr:  "1.0.0",
		},
		{
			name:     "double quoted",
			content:  "version: \"1.0.0\"\n",
			segments: []string{"version"},
			wantStr:  "1.0.0",
		},
		{
			name:     "single quoted",
			content:  "version: '1.0.0'\n",
			segments: []string{"version"},
			wantStr:  "1.0.0",
		},
		{
			name:     "nested",
			content:  "metadata:\n  version: 2.0.0\n",
			segments: []string{"metadata", "version"},
			wantStr:  "2.0.0",
		},
		{
			name:     "missing key",
			content:  "other: 1.0.0\n",
			segments: []string{"version"},
			wantErr:  true,
		},
		{
			name:     "double quoted with escape",
			content:  "desc: \"line1\\nline2\"\n",
			segments: []string{"desc"},
			wantStr:  "line1\\nline2",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			content := []byte(tt.content)
			start, end, err := findYAMLValueOffset(content, tt.segments)
			if (err != nil) != tt.wantErr {
				t.Errorf("findYAMLValueOffset() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			got := string(content[start:end])
			if got != tt.wantStr {
				t.Errorf("findYAMLValueOffset() extracted %q, want %q", got, tt.wantStr)
			}
		})
	}
}

// ---- findJSONValueOffset tests ----

func TestFindJSONValueOffset(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		content  string
		segments []string
		wantStr  string
		wantErr  bool
	}{
		{
			name:     "simple",
			content:  `{"version": "1.0.0"}`,
			segments: []string{"version"},
			wantStr:  "1.0.0",
		},
		{
			name:     "nested",
			content:  `{"metadata": {"version": "2.0.0"}}`,
			segments: []string{"metadata", "version"},
			wantStr:  "2.0.0",
		},
		{
			name:     "skips non-matching keys",
			content:  `{"name": "1.0.0", "version": "2.0.0", "desc": "x"}`,
			segments: []string{"version"},
			wantStr:  "2.0.0",
		},
		{
			name:     "missing key",
			content:  `{"other": "1.0.0"}`,
			segments: []string{"version"},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			content := []byte(tt.content)
			start, end, err := findJSONValueOffset(content, tt.segments)
			if (err != nil) != tt.wantErr {
				t.Errorf("findJSONValueOffset() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			got := string(content[start:end])
			if got != tt.wantStr {
				t.Errorf("findJSONValueOffset() extracted %q, want %q", got, tt.wantStr)
			}
		})
	}
}

// ---- lineColumnToOffset tests ----

func TestLineColumnToOffset(t *testing.T) {
	t.Parallel()
	content := []byte("abc\ndef\nghi\n")
	tests := []struct {
		name string
		line int
		col  int
		want int
	}{
		{"line 1 col 1", 1, 1, 0},
		{"line 1 col 3", 1, 3, 2},
		{"line 2 col 1", 2, 1, 4},
		{"line 3 col 2", 3, 2, 9},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := lineColumnToOffset(content, tt.line, tt.col)
			if got != tt.want {
				t.Errorf("lineColumnToOffset(line=%d, col=%d) = %d, want %d", tt.line, tt.col, got, tt.want)
			}
		})
	}
}

// ---- FindComponents tests ----

func TestFindComponents(t *testing.T) {
	t.Parallel()

	// Build a temp monorepo tree:
	//   root/
	//     templates/
	//       alpha/template.yml   (version: 1.0.0)
	//       beta/template.yml    (version: 2.3.4)
	root := t.TempDir()
	for _, comp := range []struct {
		dir     string
		version string
	}{
		{"templates/alpha", "1.0.0"},
		{"templates/beta", "2.3.4"},
	} {
		dir := filepath.Join(root, comp.dir)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		content := "version: " + comp.version + "\n"
		if err := os.WriteFile(filepath.Join(dir, "template.yml"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	cfg := MonorepoConfig{
		VersioningFile: "templates/*/template.yml",
		Path:           "version",
	}

	proc := NewMonorepoProcessor()
	components, err := proc.FindComponents(root, cfg)
	if err != nil {
		t.Fatalf("FindComponents() error = %v", err)
	}
	if len(components) != 2 {
		t.Fatalf("FindComponents() returned %d components, want 2", len(components))
	}

	// Build a lookup by name for order-independent assertions.
	byName := make(map[string]MonorepoComponent)
	for _, c := range components {
		byName[c.Name] = c
	}

	for _, tc := range []struct{ name, version string }{
		{"alpha", "1.0.0"},
		{"beta", "2.3.4"},
	} {
		c, ok := byName[tc.name]
		if !ok {
			t.Errorf("component %q not found", tc.name)
			continue
		}
		if c.CurrentVersion.Original() != tc.version {
			t.Errorf("component %q version = %v, want %v", tc.name, c.CurrentVersion.Original(), tc.version)
		}
		wantRoot := filepath.Join(root, "templates", tc.name)
		if c.RootPath != wantRoot {
			t.Errorf("component %q RootPath = %v, want %v", tc.name, c.RootPath, wantRoot)
		}
	}
}

func TestFindComponents_NoMatch(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cfg := MonorepoConfig{
		VersioningFile: "templates/*/template.yml",
		Path:           "version",
	}
	proc := NewMonorepoProcessor()
	_, err := proc.FindComponents(root, cfg)
	if err == nil {
		t.Error("FindComponents() expected error for no matches, got nil")
	}
}

func TestFindComponents_EmptyConfig(t *testing.T) {
	t.Parallel()
	proc := NewMonorepoProcessor()
	_, err := proc.FindComponents(t.TempDir(), MonorepoConfig{})
	if err == nil {
		t.Error("FindComponents() expected error for empty config, got nil")
	}
}

func TestFindComponents_MissingVersionKey(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "templates", "nokey")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	// File exists but doesn't have the version annotation.
	content := "metadata:\n  annotations:\n    other-key: value\n"
	if err := os.WriteFile(filepath.Join(dir, "template.yml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := MonorepoConfig{
		VersioningFile: "templates/*/template.yml",
		Path:           `.metadata.annotations["backstage.io/template-version"]`,
	}

	proc := NewMonorepoProcessor()
	components, err := proc.FindComponents(root, cfg)
	if err != nil {
		t.Fatalf("FindComponents() error = %v, want nil (should default to 0.0.0)", err)
	}
	if len(components) != 1 {
		t.Fatalf("FindComponents() returned %d components, want 1", len(components))
	}
	if components[0].CurrentVersion.Original() != "0.0.0" {
		t.Errorf("component version = %v, want 0.0.0", components[0].CurrentVersion.Original())
	}
	if !components[0].VersionKeyMissing {
		t.Error("component VersionKeyMissing = false, want true")
	}
}

// TestWriteVersionToFile_PreservesPermissions verifies that writeVersionToFile
// does not clobber the original file's permission bits.
func TestWriteVersionToFile_PreservesPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits are not meaningful on Windows")
	}
	t.Parallel()

	dir := t.TempDir()
	fpath := filepath.Join(dir, "test.yml")
	if err := os.WriteFile(fpath, []byte("version: 1.0.0\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := writeVersionToFile(fpath, "version", "2.0.0"); err != nil {
		t.Fatalf("writeVersionToFile() error = %v", err)
	}

	fi, err := os.Stat(fpath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0644 {
		t.Errorf("file permissions after write = %o, want 0644", perm)
	}
}

// TestInsertYAMLKey_EmptyMapping verifies that insertYAMLKey handles
// an empty flow mapping by converting it to block style.
func TestInsertYAMLKey_EmptyMapping(t *testing.T) {
	t.Parallel()
	content := []byte("metadata: {}\n")
	result, err := insertYAMLKey(content, []string{"metadata", "version"}, "1.0.0")
	if err != nil {
		t.Fatalf("insertYAMLKey() unexpected error: %v", err)
	}
	want := "metadata:\n  version: 1.0.0\n"
	if string(result) != want {
		t.Errorf("insertYAMLKey() =\n%s\nwant:\n%s", result, want)
	}
}

// TestFindComponents_CorruptYAML verifies that corrupt YAML files propagate as
// errors instead of being silently treated as missing-key.
func TestFindComponents_CorruptYAML(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "templates", "bad")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "template.yml"), []byte(":\n  :\n[broken"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := MonorepoConfig{
		VersioningFile: "templates/*/template.yml",
		Path:           "version",
	}

	proc := NewMonorepoProcessor()
	_, err := proc.FindComponents(root, cfg)
	if err == nil {
		t.Error("FindComponents() expected error for corrupt YAML, got nil")
	}
}
