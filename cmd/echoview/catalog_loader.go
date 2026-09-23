package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dayflower/echoview/internal/catalogset"
)

// catalogDirectories collects repeatable --catalog-dir values in command-line
// order. Supplying at least one value replaces the platform default locations.
type catalogDirectories []string

func (directories *catalogDirectories) String() string {
	return strings.Join(*directories, ",")
}

func (directories *catalogDirectories) Set(value string) error {
	if value == "" {
		return fmt.Errorf("--catalog-dir must not be empty")
	}
	*directories = append(*directories, value)
	return nil
}

func loadCatalogSet(directories catalogDirectories, logger *stderrLogger) (*catalogset.Set, error) {
	set, err := catalogset.Load(catalogset.Options{Directories: directories})
	if err != nil {
		return nil, err
	}
	for _, warning := range set.Warnings {
		logger.Warnf("%s", warning)
	}
	if !logger.debug {
		return set, nil
	}
	for _, file := range set.Files {
		logger.Debugf("loaded catalog file %q", file)
	}
	classIDs := make([]string, 0, len(set.Classes))
	for id := range set.Classes {
		classIDs = append(classIDs, id)
	}
	sort.Strings(classIDs)
	for _, id := range classIDs {
		logger.Debugf("class %q from %q", id, set.Classes[id].File)
	}
	profileIDs := make([]string, 0, len(set.ProfileDefs))
	for id := range set.ProfileDefs {
		profileIDs = append(profileIDs, id)
	}
	sort.Strings(profileIDs)
	for _, id := range profileIDs {
		logger.Debugf("profile %q from %q", id, set.ProfileDefs[id].File)
	}
	return set, nil
}
