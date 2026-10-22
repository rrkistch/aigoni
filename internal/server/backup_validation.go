package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"aigoni/internal/configdoc"
	"aigoni/internal/content"
	"aigoni/internal/sitebackup"
)

func (s *Server) validateBackupData(stage string, manifest sitebackup.Manifest) error {
	for _, group := range []struct {
		name string
		typ  content.Type
	}{{"posts", content.TypePost}, {"pages", content.TypePage}, {"notes", content.TypeNote}, {"dynamics", content.TypeDynamic}, {"projects", content.TypeProject}} {
		if !manifest.Roots[group.name] {
			continue
		}
		slugs := map[string]bool{}
		err := filepath.WalkDir(filepath.Join(stage, group.name), func(file string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(file) != ".md" {
				return nil
			}
			b, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			item, err := content.Parse(b, group.typ)
			if err != nil {
				return fmt.Errorf("%s 中有无效 Markdown 元数据：%s", group.name, filepath.Base(file))
			}
			if group.typ != content.TypeNote {
				if len(item.Slug) > 200 || !safeRelease(item.Slug) || slugs[item.Slug] {
					return errors.New("备份中存在无效或重复的公开链接标识")
				}
				slugs[item.Slug] = true
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	validateTheme := func(file string) error {
		doc, err := configdoc.Read(file, map[string]any{})
		if err != nil {
			return err
		}
		if s.activeThemeVersion() != "source" {
			if err = validateThemeOverrides(s.activeThemeSource(), doc.Value); err != nil {
				return err
			}
		}
		for name, value := range doc.Value {
			field, ok := adminSchema()[name]
			if !ok {
				return fmt.Errorf("备份的配置组 %s 不受当前版本支持", name)
			}
			if err = validateAdminValue(field, value, field.Label); err != nil {
				return err
			}
		}
		return nil
	}
	if manifest.Roots["theme"] {
		if err := validateTheme(filepath.Join(stage, "theme")); err != nil {
			return err
		}
	}
	if manifest.Roots["history"] {
		entries, err := os.ReadDir(filepath.Join(stage, "history"))
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
				if err = validateTheme(filepath.Join(stage, "history", entry.Name())); err != nil {
					return err
				}
			}
		}
	}
	if manifest.Roots["onboarding"] {
		doc, err := configdoc.Read(filepath.Join(stage, "onboarding"), onboardingSettings{})
		if err != nil {
			return err
		}
		for _, owned := range doc.Value.Owned {
			if !strings.HasPrefix(owned.ID, "samples/") || len(owned.Revision) != 64 || (owned.Type != content.TypeDynamic && owned.Type != content.TypeProject && owned.Type != content.TypePage) {
				return errors.New("备份中的示例归属信息无效")
			}
		}
	}
	if manifest.Roots["mediaindex"] {
		entries, err := os.ReadDir(filepath.Join(stage, "mediaindex"))
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(stage, "mediaindex", entry.Name()))
			if err != nil {
				return err
			}
			var item mediaItem
			if json.Unmarshal(b, &item) != nil || !validMediaItem(item) || entry.Name() != item.ID+".json" {
				return errors.New("备份中的素材索引无效")
			}
			info, err := os.Stat(filepath.Join(stage, "mediafiles", filepath.Base(item.Path)))
			if err != nil || !info.Mode().IsRegular() || info.Size() != item.Size {
				return errors.New("备份中的素材文件缺失或大小不一致")
			}
		}
	}
	return nil
}

// Call with themeMu/onboardingMu held. Restore is also a configuration write: revisions cannot rewind.
func (s *Server) advanceRestoredConfigRevisions(stage string, manifest *sitebackup.Manifest) error {
	current, err := s.readThemeSettings()
	if err != nil {
		return err
	}
	restored, err := configdoc.Read(filepath.Join(stage, "theme"), map[string]any{})
	if err != nil {
		return err
	}
	max := current.Revision
	if restored.Revision > max {
		max = restored.Revision
	}
	if max == ^uint64(0) {
		return errors.New("外观配置版本已超出支持范围")
	}
	restored.Revision = max + 1
	data, err := json.Marshal(restored)
	if err != nil {
		return err
	}
	if err = configdoc.Atomic(filepath.Join(stage, "theme"), data); err != nil {
		return err
	}
	manifest.Roots["theme"] = true
	setup, err := s.readOnboarding()
	if err != nil {
		return err
	}
	oldSetup, err := configdoc.Read(filepath.Join(stage, "onboarding"), onboardingSettings{})
	if err != nil {
		return err
	}
	max = setup.Revision
	if oldSetup.Revision > max {
		max = oldSetup.Revision
	}
	if max == ^uint64(0) {
		return errors.New("引导配置版本已超出支持范围")
	}
	oldSetup.Revision = max + 1
	data, err = json.Marshal(oldSetup)
	if err != nil {
		return err
	}
	if err = configdoc.Atomic(filepath.Join(stage, "onboarding"), data); err != nil {
		return err
	}
	manifest.Roots["onboarding"] = true
	return nil
}
