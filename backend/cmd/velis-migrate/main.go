package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "YAML 配置文件路径")
	migrationsPath := flag.String("path", "migrations", "迁移文件目录")
	steps := flag.Int("steps", 1, "down 时回退的版本数")
	forceVersion := flag.Int("version", -1, "force 时写入的版本号")
	flag.Parse()
	if flag.NArg() != 1 {
		return errors.New("用法: velis-migrate [参数] <up|down|version|force>")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	absPath, err := filepath.Abs(*migrationsPath)
	if err != nil {
		return fmt.Errorf("解析迁移目录: %w", err)
	}
	m, err := migrate.New("file://"+filepath.ToSlash(absPath), cfg.Database.URL)
	if err != nil {
		return fmt.Errorf("初始化迁移器: %w", err)
	}
	defer func() {
		_, _ = m.Close()
	}()

	switch flag.Arg(0) {
	case "up":
		err = m.Up()
	case "down":
		if *steps <= 0 {
			return errors.New("-steps 必须大于 0")
		}
		err = m.Steps(-*steps)
	case "version":
		version, dirty, versionErr := m.Version()
		if versionErr != nil {
			return fmt.Errorf("读取迁移版本: %w", versionErr)
		}
		fmt.Printf("version=%d dirty=%t\n", version, dirty)
		return nil
	case "force":
		if *forceVersion < 0 {
			return errors.New("force 需要非负的 -version")
		}
		err = m.Force(*forceVersion)
	default:
		return fmt.Errorf("不支持的迁移命令 %q", flag.Arg(0))
	}
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("执行迁移 %s: %w", flag.Arg(0), err)
	}
	fmt.Printf("migration %s: ok\n", flag.Arg(0))
	return nil
}
