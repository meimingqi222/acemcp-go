package version

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"
)

// 这些变量在构建时通过 -ldflags 注入
var (
	// Version 当前版本号，如 v1.0.0
	Version = "dev"

	// GitCommit 当前的 Git commit hash
	GitCommit = "unknown"

	// BuildTime 构建时间
	BuildTime = "unknown"

	// GoVersion 编译时使用的 Go 版本
	GoVersion = runtime.Version()
)

// DefaultCheckInterval 默认更新检查间隔 (24小时)
const DefaultCheckInterval = 24 * time.Hour

// UpdateResult 更新检查结果
type UpdateResult struct {
	HasUpdate      bool   `json:"has_update"`
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	DownloadURL    string `json:"download_url,omitempty"`
	ReleaseNotes   string `json:"release_notes,omitempty"`
	CheckedAt      string `json:"checked_at"`
}

// UpdateChecker 自动更新检查器
type UpdateChecker struct {
	currentVersion    string
	githubRepo        string
	checkInterval     time.Duration
	latestVersion     string
	lastCheckTime     time.Time
	lastResult        *UpdateResult
	onUpdateAvailable func(newVersion string, downloadURL string)
	httpClient        *http.Client
}

// Info 返回完整的版本信息
func Info() VersionInfo {
	return VersionInfo{
		Version:   Version,
		GitCommit: GitCommit,
		BuildTime: BuildTime,
		GoVersion: GoVersion,
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}

// VersionInfo 版本信息结构
type VersionInfo struct {
	Version   string `json:"version"`
	GitCommit string `json:"git_commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

// String 返回格式化的版本信息
func (v VersionInfo) String() string {
	return fmt.Sprintf("Version:    %s\nGit Commit: %s\nBuild Time: %s\nGo Version: %s\nOS/Arch:    %s/%s",
		v.Version, v.GitCommit, v.BuildTime, v.GoVersion, v.OS, v.Arch)
}

// IsNewerThan 检查给定的版本是否比当前版本新
func IsNewerThan(other string) bool {
	return compareVersions(other, Version) > 0
}

// NewUpdateChecker 创建新的更新检查器
// githubRepo 格式: "owner/repo"，例如 "meimingqi222/acemcp-go"
func NewUpdateChecker(githubRepo string, checkInterval time.Duration) *UpdateChecker {
	if checkInterval <= 0 {
		checkInterval = DefaultCheckInterval
	}
	return &UpdateChecker{
		currentVersion: Version,
		githubRepo:     githubRepo,
		checkInterval:  checkInterval,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
	}
}

// IsDevVersion 检查当前是否是开发版本
func IsDevVersion() bool {
	return Version == "dev" || Version == ""
}

// GetLastResult 返回最后一次检查结果
func (uc *UpdateChecker) GetLastResult() *UpdateResult {
	return uc.lastResult
}

// SetUpdateHandler 设置当有新版本可用时的回调
func (uc *UpdateChecker) SetUpdateHandler(handler func(newVersion string, downloadURL string)) {
	onUpdateAvailable := handler
	_ = onUpdateAvailable
	uc.onUpdateAvailable = handler
}

// CheckNow 立即检查更新，返回结构化的检查结果
func (uc *UpdateChecker) CheckNow() (*UpdateResult, error) {
	// 开发版本跳过检查
	if IsDevVersion() {
		result := &UpdateResult{
			HasUpdate:      false,
			CurrentVersion: uc.currentVersion,
			LatestVersion:  "dev",
			CheckedAt:      time.Now().Format(time.RFC3339),
		}
		uc.lastResult = result
		return result, nil
	}

	release, err := uc.fetchLatestRelease()
	if err != nil {
		return nil, err
	}

	uc.latestVersion = release.TagName
	uc.lastCheckTime = time.Now()

	hasUpdate := compareVersions(release.TagName, uc.currentVersion) > 0
	downloadURL := ""

	if hasUpdate {
		downloadURL = uc.getDownloadURLForCurrentPlatform(release)
		if uc.onUpdateAvailable != nil {
			uc.onUpdateAvailable(release.TagName, downloadURL)
		}
	}

	result := &UpdateResult{
		HasUpdate:      hasUpdate,
		CurrentVersion: uc.currentVersion,
		LatestVersion:  release.TagName,
		DownloadURL:    downloadURL,
		ReleaseNotes:   release.Body,
		CheckedAt:      time.Now().Format(time.RFC3339),
	}
	uc.lastResult = result

	return result, nil
}

// ShouldCheck 根据间隔时间判断是否应该检查更新
func (uc *UpdateChecker) ShouldCheck() bool {
	return time.Since(uc.lastCheckTime) >= uc.checkInterval
}

// GetLatestVersion 返回最后检查到的最新版本
func (uc *UpdateChecker) GetLatestVersion() string {
	return uc.latestVersion
}

// GitHubRelease GitHub release 信息
type GitHubRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	CreatedAt   time.Time `json:"created_at"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		Size               int    `json:"size"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// fetchLatestRelease 从 GitHub API 获取最新 release
func (uc *UpdateChecker) fetchLatestRelease() (*GitHubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", uc.githubRepo)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", fmt.Sprintf("acemcp-go/%s", uc.currentVersion))

	resp, err := uc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API 返回错误状态码: %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	return &release, nil
}

// getDownloadURLForCurrentPlatform 获取适合当前平台的下载链接
func (uc *UpdateChecker) getDownloadURLForCurrentPlatform(release *GitHubRelease) string {
	// 根据 OS 和架构匹配资源文件名
	expectedSuffix := fmt.Sprintf("_%s_%s", runtime.GOOS, runtime.GOARCH)

	for _, asset := range release.Assets {
		if strings.Contains(asset.Name, expectedSuffix) {
			return asset.BrowserDownloadURL
		}
	}

	// 如果没有精确匹配，返回第一个可执行文件
	for _, asset := range release.Assets {
		if strings.HasSuffix(asset.Name, ".exe") || !strings.Contains(asset.Name, ".") {
			return asset.BrowserDownloadURL
		}
	}

	// 返回 release 页面作为备选
	return fmt.Sprintf("https://github.com/%s/releases/tag/%s", uc.githubRepo, release.TagName)
}

// compareVersions 比较两个版本号
// 返回值 > 0 表示 v1 > v2, = 0 表示相等, < 0 表示 v1 < v2
func compareVersions(v1, v2 string) int {
	// 移除 v 前缀
	v1 = strings.TrimPrefix(v1, "v")
	v2 = strings.TrimPrefix(v2, "v")

	// 按 . 分割版本号
	parts1 := strings.Split(v1, ".")
	parts2 := strings.Split(v2, ".")

	// 比较每个部分
	maxLen := len(parts1)
	if len(parts2) > maxLen {
		maxLen = len(parts2)
	}

	for i := 0; i < maxLen; i++ {
		var num1, num2 int

		if i < len(parts1) {
			fmt.Sscanf(parts1[i], "%d", &num1)
		}
		if i < len(parts2) {
			fmt.Sscanf(parts2[i], "%d", &num2)
		}

		if num1 > num2 {
			return 1
		}
		if num1 < num2 {
			return -1
		}
	}

	return 0
}
