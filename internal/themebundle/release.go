package themebundle

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const Repository = "rrkistch/aigoni"

func AssetName(tag string) string { return "aigoni-" + tag + "-linux-amd64.tar.gz" }

// TrustedDigest uses GitHub's asset digest from the fixed repository, never a package-supplied URL.
func TrustedDigest(ctx context.Context, client *http.Client, tag string, size int64) (string, error) {
	if !TagPattern.MatchString(tag) {
		return "", errors.New("无效主题发布标签")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/"+Repository+"/releases/tags/"+tag, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "aigoni-theme-installer")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("无法连接 GitHub 校验主题来源，请检查网络后重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", errors.New("GitHub 发布信息不可用，请确认标签已发布并稍后重试")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return "", errors.New("GitHub 发布响应无效")
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name   string `json:"name"`
			Size   int64  `json:"size"`
			Digest string `json:"digest"`
			URL    string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if json.Unmarshal(data, &release) != nil || release.Tag != tag || release.Draft || release.Prerelease {
		return "", errors.New("只接受固定仓库已发布的正式主题版本")
	}
	name := AssetName(tag)
	for _, asset := range release.Assets {
		if asset.Name != name {
			continue
		}
		digest := strings.TrimPrefix(asset.Digest, "sha256:")
		decoded, e := hex.DecodeString(digest)
		if asset.Size != size || e != nil || len(decoded) != 32 || asset.Digest != "sha256:"+digest || asset.URL != "https://github.com/"+Repository+"/releases/download/"+tag+"/"+name {
			return "", errors.New("发布文件大小、来源或 GitHub 校验值不匹配")
		}
		return strings.ToLower(digest), nil
	}
	return "", errors.New("此发布版本没有受支持的 Linux amd64 主题包")
}
