package themebundle

import (
	"aigoni/internal/sitebackup"
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const MaxArchive int64 = 1 << 30
const MaxExpanded int64 = 4 << 30
const MaxEntries = 250000
const PNPM = "11.22.0"

var TagPattern = regexp.MustCompile(`^theme-v[0-9]+\.[0-9]+\.[0-9]+$`)
var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

type Manifest struct {
	Assets       string `json:"assets"`
	Dependencies string `json:"dependencies"`
	Format       int    `json:"format"`
	Version      string `json:"version"`
	SourceCommit string `json:"source_commit"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	NodeMajor    int    `json:"node_major"`
	PNPM         string `json:"pnpm"`
	AdminAPI     int    `json:"admin_api"`
	Settings     int    `json:"settings"`
}

func (m Manifest) Validate(tag string) error {
	if !TagPattern.MatchString(tag) || m.Version != tag || m.Format != 1 || m.Dependencies != "bundled-portable-v1" || m.Assets != "original-fonts-v1" || !commitPattern.MatchString(m.SourceCommit) || m.OS != "linux" || m.Arch != "amd64" || m.NodeMajor != 24 || m.PNPM != PNPM || (m.AdminAPI < 1 || m.AdminAPI > 2) || m.Settings != 1 {
		return errors.New("主题包格式或运行环境不兼容（需要 Linux amd64 / Node 24 / 当前配置接口）")
	}
	return nil
}
func Within(root, name string) bool {
	rel, err := filepath.Rel(root, name)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func safeName(name string) bool {
	return name != "" && name != "." && path.Clean(name) == name && !strings.HasPrefix(name, "/") && name != ".." && !strings.HasPrefix(name, "../") && !strings.ContainsAny(name, "\\:\x00")
}

// Extract never executes package scripts. All links are deferred until regular files exist.
func Extract(ctx context.Context, archive, dest, tag string) (Manifest, int64, error) {
	var manifest Manifest
	file, err := os.Open(archive)
	if err != nil {
		return manifest, 0, err
	}
	defer file.Close()
	buffer := bufio.NewReader(file)
	gz, err := gzip.NewReader(buffer)
	if err != nil {
		return manifest, 0, err
	}
	defer gz.Close()
	gz.Multistream(false)
	stream := &io.LimitedReader{R: contextReader{ctx, gz}, N: MaxExpanded + (128 << 20)}
	tr := tar.NewReader(stream)
	seen := map[string]byte{}
	links := map[string]string{}
	var total int64
	hasManifest := false
	if err = os.MkdirAll(dest, 0700); err != nil {
		return manifest, 0, err
	}
	free, err := sitebackup.FreeBytes(dest)
	if err != nil || free < 128<<20 {
		return manifest, 0, errors.New("可用空间不足")
	}
	budget := free - (128 << 20)
	for count := 0; ; count++ {
		if err = ctx.Err(); err != nil {
			return manifest, total, err
		}
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return manifest, total, e
		}
		name := strings.TrimSuffix(h.Name, "/")
		if count >= MaxEntries || !safeName(name) || seen[name] != 0 || (name != "manifest.json" && name != "source" && !strings.HasPrefix(name, "source/")) {
			return manifest, total, errors.New("主题包包含无效路径、重复项或过多文件")
		}
		if h.Size < 0 || h.Size > MaxExpanded-total || h.Size > budget-total {
			return manifest, total, errors.New("主题包解压超过 4 GiB")
		}
		total += h.Size
		target := filepath.Join(dest, filepath.FromSlash(name))
		if !Within(dest, target) {
			return manifest, total, errors.New("主题路径越界")
		}
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if seen[parent] != 0 && seen[parent] != tar.TypeDir {
				return manifest, total, errors.New("主题文件穿越链接或普通文件")
			}
		}
		seen[name] = h.Typeflag
		switch h.Typeflag {
		case tar.TypeDir:
			if name == "manifest.json" {
				return manifest, total, errors.New("无效主题清单")
			}
			err = os.MkdirAll(target, 0755)
		case tar.TypeReg, tar.TypeRegA:
			if name == "source" {
				return manifest, total, errors.New("source 必须为目录")
			}
			if name == "manifest.json" {
				if h.Size > 65536 {
					return manifest, total, errors.New("主题清单过大")
				}
				data, e := io.ReadAll(tr)
				if e != nil {
					return manifest, total, e
				}
				if err = json.Unmarshal(data, &manifest); err != nil {
					return manifest, total, err
				}
				if err = manifest.Validate(tag); err != nil {
					return manifest, total, err
				}
				hasManifest = true
			} else {
				if err = os.MkdirAll(filepath.Dir(target), 0755); err == nil {
					var out *os.File
					out, err = os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
					if err == nil {
						_, err = io.Copy(out, tr)
						closeErr := out.Close()
						if err == nil {
							err = closeErr
						}
						if err == nil {
							err = os.Chmod(target, 0644|os.FileMode(h.Mode&0111))
						}
					}
				}
			}
		case tar.TypeSymlink:
			resolved := path.Clean(path.Join(path.Dir(name), h.Linkname))
			if !strings.HasPrefix(name, "source/node_modules/") || path.IsAbs(h.Linkname) || strings.ContainsAny(h.Linkname, "\\:\x00") || !strings.HasPrefix(resolved, "source/node_modules/") {
				return manifest, total, errors.New("主题包包含越界链接")
			}
			if _, e := os.Lstat(target); e == nil {
				return manifest, total, errors.New("链接覆盖已有目录")
			}
			links[name] = h.Linkname
		default:
			return manifest, total, errors.New("主题包不允许硬链接、设备或特殊文件")
		}
		if err != nil {
			return manifest, total, err
		}
	}
	if stream.N <= 0 {
		return manifest, total, errors.New("主题包数据超过上限")
	}
	if _, err = io.Copy(io.Discard, stream); err != nil || stream.N <= 0 {
		return manifest, total, errors.New("主题压缩数据损坏或超过上限")
	}
	if _, e := buffer.Peek(1); e != io.EOF {
		return manifest, total, errors.New("主题包包含额外压缩成员或尾随数据")
	}
	if !hasManifest {
		return manifest, total, errors.New("缺少主题清单")
	}
	for name, link := range links {
		target := filepath.Join(dest, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return manifest, total, err
		}
		if err = os.Symlink(filepath.FromSlash(link), target); err != nil {
			return manifest, total, err
		}
	}
	for name := range links {
		resolved, e := filepath.EvalSymlinks(filepath.Join(dest, filepath.FromSlash(name)))
		if e != nil || !Within(filepath.Join(dest, "source", "node_modules"), resolved) {
			return manifest, total, errors.New("主题链接损坏、成环或越界")
		}
	}
	for _, name := range []string{"package.json", "pnpm-lock.yaml", "scripts/aigoni-build.mjs", "aigoni-defaults.json", "aigoni-settings-schema.json", "node_modules/astro/package.json"} {
		info, e := os.Stat(filepath.Join(dest, "source", filepath.FromSlash(name)))
		if e != nil || !info.Mode().IsRegular() {
			return manifest, total, fmt.Errorf("主题包缺少 %s", name)
		}
	}
	return manifest, total, nil
}

// Copy makes a writable workspace without hard-linking mutable files back into the installed version.
func Copy(ctx context.Context, source, dest string) error {
	return filepath.Walk(source, func(file string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(source, file)
		if err != nil {
			return err
		}
		out := filepath.Join(dest, rel)
		if info.IsDir() {
			return os.MkdirAll(out, 0755)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, e := os.Readlink(file)
			if e != nil {
				return e
			}
			return os.Symlink(link, out)
		}
		if !info.Mode().IsRegular() {
			return errors.New("installed theme contains a special file")
		}
		in, e := os.Open(file)
		if e != nil {
			return e
		}
		defer in.Close()
		to, e := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644|info.Mode().Perm()&0111)
		if e != nil {
			return e
		}
		_, e = io.Copy(to, in)
		ce := to.Close()
		if e == nil {
			e = ce
		}
		return e
	})
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
