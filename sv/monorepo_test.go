package sv

import (
	"os"
	"path/filepath"
	"testing"
)

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
			name: "key with dot via greedy match",
			data: map[string]interface{}{
				"metadata": map[string]interface{}{
					"annotations": map[string]interface{}{
						"backstage.io/template-version": "3.1.0",
					},
				},
			},
			segments: []string{"metadata", "annotations", "backstage", "io/template-version"},
			want:     "3.1.0",
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
			name: "key with dot via greedy match",
			data: map[string]interface{}{
				"annotations": map[string]interface{}{
					"backstage.io/template-version": "0.0.1",
				},
			},
			segments: []string{"annotations", "backstage", "io/template-version"},
			value:    "1.0.0",
			wantVal:  "1.0.0",
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
			name:    "backstage yaml with dot key",
			ext:     ".yml",
			content: "metadata:\n  annotations:\n    backstage.io/template-version: 3.1.4\n",
			dotPath: "metadata.annotations.backstage.io/template-version",
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
		name    string
		ext     string
		content string
		dotPath string
		version string
		wantErr bool
	}{
		{
			name:    "yaml round-trip",
			ext:     ".yml",
			content: "version: 1.0.0\n",
			dotPath: "version",
			version: "1.1.0",
		},
		{
			name:    "json round-trip",
			ext:     ".json",
			content: `{"version": "1.0.0"}`,
			dotPath: "version",
			version: "2.0.0",
		},
		{
			name:    "nested yaml round-trip",
			ext:     ".yaml",
			content: "metadata:\n  version: 0.1.0\n",
			dotPath: "metadata.version",
			version: "0.2.0",
		},
		{
			name:    "missing path returns error",
			ext:     ".yml",
			content: "other: value\n",
			dotPath: "version",
			version: "1.0.0",
			wantErr: true,
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
