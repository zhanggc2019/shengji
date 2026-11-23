// Package update looks up and verifies Windows installers published on GitHub Releases.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const maxInstallerBytes = 512 << 20

// Installer describes a verified release asset before it is downloaded.
type Installer struct {
	Version string
	name    string
	url     string
	size    int64
	digest  string
}

var client = &http.Client{CheckRedirect: func(req *http.Request, previous []*http.Request) error {
	if len(previous) >= 5 || req.URL.Scheme != "https" || req.URL.User != nil {
		return errors.New("更新服务返回了无效的跳转地址")
	}
	switch req.URL.Host {
	case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return nil
	default:
		return errors.New("更新服务返回了非 GitHub 的下载地址")
	}
}}

// Check finds the newest stable installer matching the application's build.
// A nil installer means no newer release is available.
func Check(ctx context.Context, repository, current, architecture string, offline bool) (*Installer, error) {
	return check(ctx, client, repository, current, architecture, offline)
}

func check(ctx context.Context, httpClient *http.Client, repository, current, architecture string, offline bool) (*Installer, error) {
	name := "Shengji-" + architecture
	if offline {
		name += "-offline"
	}
	name += "-installer.exe"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+repository+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotFound:
		return nil, nil
	case http.StatusOK:
	default:
		return nil, fmt.Errorf("GitHub 返回 HTTP %d", resp.StatusCode)
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Size   int64  `json:"size"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&release); err != nil {
		return nil, fmt.Errorf("读取 GitHub 发布信息: %w", err)
	}
	if release.Draft || release.Prerelease || !newer(strings.TrimPrefix(release.Tag, "v"), current) {
		return nil, nil
	}
	for _, asset := range release.Assets {
		if asset.Name != name {
			continue
		}
		link, err := url.Parse(asset.URL)
		expected := "/" + repository + "/releases/download/" + release.Tag + "/" + name
		if err != nil || link.Scheme != "https" || link.Host != "github.com" || link.User != nil || link.Path != expected || link.RawQuery != "" || link.Fragment != "" {
			return nil, errors.New("安装包地址与 GitHub 发布信息不匹配")
		}
		digest, ok := strings.CutPrefix(asset.Digest, "sha256:")
		if _, err := hex.DecodeString(digest); !ok || err != nil || len(digest) != sha256.Size*2 || asset.Size <= 0 || asset.Size > maxInstallerBytes {
			return nil, errors.New("GitHub 发布的安装包缺少有效的大小或 SHA-256 校验值")
		}
		return &Installer{Version: strings.TrimPrefix(release.Tag, "v"), name: name, url: link.String(), size: asset.Size, digest: strings.ToLower(digest)}, nil
	}
	return nil, fmt.Errorf("GitHub 发布 %s 缺少当前版本所需的安装包 %s", release.Tag, name)
}

func newer(candidate, current string) bool {
	parse := func(version string) ([3]uint64, bool) {
		var result [3]uint64
		parts := strings.Split(version, ".")
		if len(parts) != len(result) {
			return result, false
		}
		for i, part := range parts {
			n, err := strconv.ParseUint(part, 10, 16)
			if err != nil || strconv.FormatUint(n, 10) != part {
				return result, false
			}
			result[i] = n
		}
		return result, true
	}
	next, validNext := parse(candidate)
	installed, validInstalled := parse(current)
	if !validNext || !validInstalled {
		return false
	}
	for i := range next {
		if next[i] != installed[i] {
			return next[i] > installed[i]
		}
	}
	return false
}

// Download stages a complete installer in a fresh temporary directory and checks its SHA-256.
func (i *Installer) Download(ctx context.Context) (string, error) {
	return i.download(ctx, client)
}

func (i *Installer) download(ctx context.Context, httpClient *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, i.url, nil)
	if err != nil {
		return "", err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载安装包返回 HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != i.size {
		return "", errors.New("安装包大小与发布信息不符")
	}
	dir, err := os.MkdirTemp("", "shengji-update-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, i.name)
	verified := false
	defer func() {
		if !verified {
			Remove(path)
		}
	}()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(resp.Body, i.size+1))
	if err := errors.Join(copyErr, file.Close()); err != nil {
		return "", err
	}
	if size != i.size || hex.EncodeToString(hash.Sum(nil)) != i.digest {
		return "", errors.New("安装包校验失败，请重新下载")
	}
	verified = true
	return path, nil
}

// Remove cleans up an installer staged by Download before it has been handed off to Windows.
func Remove(path string) {
	_ = os.Remove(path)
	_ = os.Remove(filepath.Dir(path))
}
