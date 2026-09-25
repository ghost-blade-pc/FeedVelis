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

// forbiddenDependencies 是不得出现在 Domain 与 Application 的技术依赖前缀。
// 分层测试只检查本模块内部的层间方向；这里额外保证 SQL、HTTP、消息、对象存储、
// 指标与第三方 SDK 类型继续留在 Infrastructure/Interfaces，而不是渗进业务规则。
var forbiddenDependencies = []string{
	"database/sql",
	"net/http",
	"net/rpc",
	"github.com/jackc/pgx",
	"github.com/golang-migrate",
	"github.com/rabbitmq",
	"github.com/minio",
	"github.com/prometheus",
	"github.com/opensearch-project",
	"github.com/cloudwego",
	"github.com/mmcdole",
	"github.com/yuin/goldmark",
	"github.com/microcosm-cc",
}

func TestDomainAndApplicationStayTechnologyFree(t *testing.T) {
	internalRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	for _, layer := range []string{"domain", "application"} {
		layerRoot := filepath.Join(internalRoot, layer)
		err := filepath.WalkDir(layerRoot, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, spec := range file.Imports {
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				for _, forbidden := range forbiddenDependencies {
					if importPath == forbidden || strings.HasPrefix(importPath, forbidden+"/") {
						rel, _ := filepath.Rel(internalRoot, path)
						t.Errorf("%s: %s 层不得依赖 %s", filepath.ToSlash(rel), layer, importPath)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
