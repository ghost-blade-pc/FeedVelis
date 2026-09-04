package architecture

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modulePrefix = "github.com/ghost-blade-pc/Velis_Feed/backend/internal/"

func TestLayerDependencies(t *testing.T) {
	internalRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}

	err = filepath.WalkDir(internalRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		rel, err := filepath.Rel(internalRoot, path)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) < 2 {
			return nil
		}
		importerLayer := parts[0]
		if importerLayer == "bootstrap" || importerLayer == "architecture" {
			return nil
		}

		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil || !strings.HasPrefix(importPath, modulePrefix) {
				continue
			}
			importedLayer := strings.Split(strings.TrimPrefix(importPath, modulePrefix), "/")[0]
			if !allowed(importerLayer, importedLayer) {
				t.Errorf("%s: %s 层不得导入 %s 层 (%s)", rel, importerLayer, importedLayer, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func allowed(importer, imported string) bool {
	switch importer {
	case "domain":
		return imported == "domain"
	case "application":
		return imported == "application" || imported == "domain"
	case "infrastructure":
		return imported == "infrastructure" || imported == "application" || imported == "domain"
	case "interfaces":
		return imported == "interfaces" || imported == "application" || imported == "domain"
	default:
		return true
	}
}
