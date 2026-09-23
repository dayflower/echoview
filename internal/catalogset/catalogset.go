// Package catalogset loads the directories that make up an ECHONET Lite
// catalog set. It owns directory ordering and document merging; catalog and
// profile packages own their respective validation and inheritance rules.
package catalogset

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/dayflower/echoview/internal/catalog"
	"github.com/dayflower/echoview/internal/profile"
	"gopkg.in/yaml.v3"
)

const (
	applicationDirectory = "echoview"
	catalogDirectory     = "catalog"
)

// Options controls how a catalog set is located. Directories replaces the
// platform defaults when it is non-empty. OS, ExecutablePath, and UserConfigDir
// are optional test seams; empty values are obtained from the current process.
type Options struct {
	Directories    []string
	OS             string
	ExecutablePath string
	UserConfigDir  string
}

// Warning describes a directory which could not contribute a catalog file.
// Such conditions do not prevent an empty catalog from being used.
type Warning struct {
	Directory string
	Message   string
}

func (w Warning) Error() string {
	return fmt.Sprintf("catalog directory %q: %s", w.Directory, w.Message)
}

// Provenance records the last input document that supplied a merged item.
type Provenance struct {
	File string
}

// Set contains the resolved class and profile catalogs from one ordered set of
// directories. Files are in the order in which they were read.
type Set struct {
	Catalog     *catalog.Catalog
	Profiles    *profile.Catalog
	Files       []string
	Warnings    []Warning
	Classes     map[string]Provenance
	ProfileDefs map[string]Provenance
}

// DefaultDirectories resolves the standard low-to-high precedence directories.
func DefaultDirectories(options Options) ([]string, error) {
	goos := options.OS
	if goos == "" {
		goos = runtime.GOOS
	}
	executable := options.ExecutablePath
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve executable path: %w", err)
		}
	}
	userConfig := options.UserConfigDir
	if userConfig == "" {
		var err error
		userConfig, err = os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("resolve user config directory: %w", err)
		}
	}
	var system string
	switch goos {
	case "darwin":
		system = filepath.Join("/Library/Application Support", applicationDirectory, catalogDirectory)
	case "windows":
		programData := os.Getenv("ProgramData")
		if programData == "" {
			return nil, fmt.Errorf("resolve ProgramData for Windows catalog directory")
		}
		system = filepath.Join(programData, applicationDirectory, catalogDirectory)
	default:
		system = filepath.Join("/etc", applicationDirectory, catalogDirectory)
	}
	return []string{system, filepath.Join(filepath.Dir(executable), catalogDirectory), filepath.Join(userConfig, applicationDirectory, catalogDirectory)}, nil
}

// Load reads all matching files once, merges their classes and profiles, and
// resolves inheritance only after every document has been merged.
func Load(options Options) (*Set, error) {
	directories := options.Directories
	if len(directories) == 0 {
		var err error
		directories, err = DefaultDirectories(options)
		if err != nil {
			return nil, err
		}
	}
	classes := map[string]any{}
	profiles := map[string]any{}
	classSources := map[string]Provenance{}
	profileSources := map[string]Provenance{}
	result := &Set{Classes: classSources, ProfileDefs: profileSources}
	for _, directory := range directories {
		files, warning, err := catalogFiles(directory)
		if err != nil {
			return nil, err
		}
		if warning != nil {
			result.Warnings = append(result.Warnings, *warning)
			continue
		}
		for _, path := range files {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read catalog %q: %w", path, err)
			}
			doc, err := parseDocument(data, path)
			if err != nil {
				return nil, err
			}
			mergeClasses(classes, doc.Classes, path, classSources)
			mergeProfiles(profiles, doc.Profiles, path, profileSources)
			result.Files = append(result.Files, path)
		}
	}
	merged, err := yaml.Marshal(map[string]any{"catalog_format": 1, "classes": classes, "profiles": profiles})
	if err != nil {
		return nil, fmt.Errorf("encode merged catalog: %w", err)
	}
	result.Catalog, err = catalog.LoadYAML(merged, "merged catalog set")
	if err != nil {
		return nil, catalogError(err, result)
	}
	result.Profiles, err = profile.LoadYAML(merged, "merged catalog set")
	if err != nil {
		return nil, catalogError(err, result)
	}
	return result, nil
}

func catalogError(err error, set *Set) error {
	message := err.Error()
	for id, provenance := range set.Classes {
		if strings.Contains(message, id) {
			return fmt.Errorf("%w (from %q)", err, provenance.File)
		}
	}
	for id, provenance := range set.ProfileDefs {
		if strings.Contains(message, id) {
			return fmt.Errorf("%w (from %q)", err, provenance.File)
		}
	}
	if len(set.Files) > 0 {
		return fmt.Errorf("%w (while resolving files through %q)", err, set.Files[len(set.Files)-1])
	}
	return err
}

type sourceDocument struct {
	CatalogFormat *int           `yaml:"catalog_format"`
	Classes       map[string]any `yaml:"classes"`
	Profiles      map[string]any `yaml:"profiles"`
}

func parseDocument(data []byte, path string) (sourceDocument, error) {
	var root yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&root); err != nil {
		return sourceDocument{}, fmt.Errorf("parse catalog %q: %w", path, err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return sourceDocument{}, fmt.Errorf("parse catalog %q: multiple YAML documents are not supported", path)
		}
		return sourceDocument{}, fmt.Errorf("parse catalog %q: %w", path, err)
	}
	if len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return sourceDocument{}, fmt.Errorf("parse catalog %q: document root must be a mapping", path)
	}
	var doc sourceDocument
	if err := root.Content[0].Decode(&doc); err != nil {
		return sourceDocument{}, fmt.Errorf("parse catalog %q: %w", path, err)
	}
	if doc.CatalogFormat == nil || *doc.CatalogFormat != 1 {
		return sourceDocument{}, fmt.Errorf("unsupported catalog format in %q", path)
	}
	if doc.Classes == nil {
		doc.Classes = map[string]any{}
	}
	if doc.Profiles == nil {
		doc.Profiles = map[string]any{}
	}
	for id, value := range doc.Classes {
		if _, ok := value.(map[string]any); !ok {
			return sourceDocument{}, fmt.Errorf("catalog %q class %q must be a mapping", path, id)
		}
	}
	for id, value := range doc.Profiles {
		if _, ok := value.(map[string]any); !ok {
			return sourceDocument{}, fmt.Errorf("catalog %q profile %q must be a mapping", path, id)
		}
	}
	return doc, nil
}

func catalogFiles(directory string) ([]string, *Warning, error) {
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil, &Warning{Directory: directory, Message: "does not exist"}, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read catalog directory %q: %w", directory, err)
	}
	paths := make([]string, 0)
	for _, entry := range entries {
		extension := filepath.Ext(entry.Name())
		if extension != ".yaml" && extension != ".yml" {
			continue
		}
		info, err := os.Stat(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, nil, fmt.Errorf("stat catalog file %q: %w", filepath.Join(directory, entry.Name()), err)
		}
		if info.Mode().IsRegular() {
			paths = append(paths, filepath.Join(directory, entry.Name()))
		}
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, &Warning{Directory: directory, Message: "contains no .yaml or .yml files"}, nil
	}
	return paths, nil, nil
}

func mergeClasses(destination map[string]any, incoming map[string]any, path string, sources map[string]Provenance) {
	for id, value := range incoming {
		previous, exists := destination[id]
		if exists {
			destination[id] = mergeClass(asMap(previous), asMap(value))
		} else {
			destination[id] = value
		}
		sources[id] = Provenance{File: path}
	}
}

func mergeProfiles(destination map[string]any, incoming map[string]any, path string, sources map[string]Provenance) {
	for id, value := range incoming {
		previous, exists := destination[id]
		if exists {
			destination[id] = mergeProfile(asMap(previous), asMap(value))
		} else {
			destination[id] = value
		}
		sources[id] = Provenance{File: path}
	}
}

func mergeClass(base, overlay map[string]any) map[string]any {
	merged := cloneMap(base)
	for key, value := range overlay {
		if key == "properties" {
			merged[key] = mergeMapEntries(asMap(merged[key]), asMap(value))
			continue
		}
		merged[key] = value
	}
	return merged
}

func mergeProfile(base, overlay map[string]any) map[string]any {
	merged := cloneMap(base)
	for key, value := range overlay {
		switch key {
		case "match":
			merged[key] = mergeMatch(asMap(merged[key]), asMap(value))
		case "collection":
			merged[key] = mergeCollection(asMap(merged[key]), asMap(value))
		case "properties":
			merged[key] = mergeProfileProperties(asMap(merged[key]), asMap(value))
		default:
			merged[key] = value
		}
	}
	return merged
}

func mergeMatch(base, overlay map[string]any) map[string]any {
	merged := cloneMap(base)
	for key, value := range overlay {
		if key == "required" || key == "optional" {
			merged[key] = mergeMapEntries(asMap(merged[key]), asMap(value))
		} else {
			merged[key] = value
		}
	}
	return merged
}

func mergeCollection(base, overlay map[string]any) map[string]any {
	merged := cloneMap(base)
	for key, value := range overlay {
		if key == "properties" {
			merged[key] = mergeMapEntries(asMap(merged[key]), asMap(value))
		} else {
			merged[key] = value
		}
	}
	return merged
}

func mergeProfileProperties(base, overlay map[string]any) map[string]any {
	merged := cloneMap(base)
	for epc, value := range overlay {
		patch := asMap(value)
		if _, hasCodec := patch["codec"]; hasCodec {
			merged[epc] = value
			continue
		}
		merged[epc] = mergeMapEntries(asMap(merged[epc]), patch)
	}
	return merged
}

func mergeMapEntries(base, overlay map[string]any) map[string]any {
	merged := cloneMap(base)
	for key, value := range overlay {
		merged[key] = value
	}
	return merged
}

func asMap(value any) map[string]any {
	if mapped, ok := value.(map[string]any); ok {
		return mapped
	}
	return map[string]any{}
}

func cloneMap(source map[string]any) map[string]any {
	copy := make(map[string]any, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}
