package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Dependency represents a dependency in GLITCH_DEPS.json
type Dependency struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Source      string `json:"source"`
	Type        string `json:"type,omitempty"`         // "binary" or "repository"
	AssetSuffix string `json:"asset_suffix,omitempty"` // e.g. "linux_amd64", "windows_amd64", "darwin_amd64"
	Private     bool   `json:"private,omitempty"`      // true for private repositories
}

// LockDependency represents a dependency in GLITCH_DEPS-lock.json
type LockDependency struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Source    string    `json:"source"`
	Version   string    `json:"version"`
	Hash      string    `json:"hash"`
	UpdatedAt time.Time `json:"updated_at"`
	Type      string    `json:"type"`
	Private   bool      `json:"private,omitempty"`
}

// GitHubRelease structure for GitHub API response
type GitHubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		ID                 int    `json:"id"`
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// DepsFile structure for GLITCH_DEPS.json
type DepsFile map[string]Dependency

// LockFile structure for GLITCH_DEPS-lock.json
type LockFile map[string]LockDependency

const (
	DepsFileName = "GLITCH_DEPS.json"
	LockFileName = "GLITCH_DEPS-lock.json"
)

// PackageManager main package manager
type PackageManager struct {
	workDir     string
	githubToken string
}

// NewPackageManager creates a new instance of the manager
func NewPackageManager() *PackageManager {
	wd, err := os.Getwd()
	if err != nil {
		log.Fatal("Failed to get working directory:", err)
	}
	
	// Read GitHub token from environment variable
	githubToken := os.Getenv("GLITCH_DEPS_GITHUB_PAT")
	
	return &PackageManager{
		workDir:     wd,
		githubToken: githubToken,
	}
}

// loadDepsFile loads GLITCH_DEPS.json
func (pm *PackageManager) loadDepsFile() (DepsFile, error) {
	depsPath := filepath.Join(pm.workDir, DepsFileName)
	data, err := ioutil.ReadFile(depsPath)
	if err != nil {
		return nil, err
	}

	var deps DepsFile
	err = json.Unmarshal(data, &deps)
	return deps, err
}

// loadLockFile loads GLITCH_DEPS-lock.json
func (pm *PackageManager) loadLockFile() (LockFile, error) {
	lockPath := filepath.Join(pm.workDir, LockFileName)
	data, err := ioutil.ReadFile(lockPath)
	if err != nil {
		return make(LockFile), nil // Return empty lock if file doesn't exist
	}

	var lock LockFile
	err = json.Unmarshal(data, &lock)
	if err != nil {
		return make(LockFile), nil
	}
	return lock, nil
}

// saveLockFile saves GLITCH_DEPS-lock.json
func (pm *PackageManager) saveLockFile(lock LockFile) error {
	lockPath := filepath.Join(pm.workDir, LockFileName)
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(lockPath, data, 0644)
}

// extractRepoInfo extracts repository information from URL
func (pm *PackageManager) extractRepoInfo(source string) (string, string, error) {
	// Parse URL format https://github.com/owner/repo.git
	re := regexp.MustCompile(`github\.com/([^/]+)/([^/]+)(?:\.git)?`)
	matches := re.FindStringSubmatch(source)
	if len(matches) < 3 {
		return "", "", fmt.Errorf("invalid GitHub URL format: %s", source)
	}
	return matches[1], strings.TrimSuffix(matches[2], ".git"), nil
}

// createAuthenticatedRequest creates an HTTP request with authorization for private repositories
func (pm *PackageManager) createAuthenticatedRequest(method, url string) (*http.Request, error) {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, err
	}
	
	// Add token if it exists
	if pm.githubToken != "" {
		req.Header.Set("Authorization", "Bearer "+pm.githubToken)
	}
	
	// GitHub releases API needs a special Accept header
	if strings.Contains(url, "releases/download") {
		req.Header.Set("Accept", "application/octet-stream")
	}
	
	return req, nil
}

// getLatestRelease gets the latest release information from GitHub API
func (pm *PackageManager) getLatestRelease(owner, repo string, isPrivate bool) (*GitHubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	
	// For private repositories, check for token presence
	if isPrivate && pm.githubToken == "" {
		return nil, fmt.Errorf("private repository %s/%s requires GLITCH_DEPS_GITHUB_PAT", owner, repo)
	}
	
	req, err := pm.createAuthenticatedRequest("GET", url)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}
	
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get release info: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("repository %s/%s not found or no access", owner, repo)
	}
	
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release GitHubRelease
	err = json.NewDecoder(resp.Body).Decode(&release)
	if err != nil {
		return nil, fmt.Errorf("failed to parse GitHub API response: %v", err)
	}

	return &release, nil
}

// downloadAssetViaAPI downloads asset via GitHub API
func (pm *PackageManager) downloadAssetViaAPI(owner, repo string, assetID int, targetPath string, isPrivate bool) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/assets/%d", owner, repo, assetID)
	fmt.Printf("Downloading via API: %s...\n", url)
	
	req, err := pm.createAuthenticatedRequest("GET", url)
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}
	
	req.Header.Set("Accept", "application/octet-stream")
	
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download file: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	err = os.MkdirAll(filepath.Dir(targetPath), 0755)
	if err != nil {
		return fmt.Errorf("failed to create directory: %v", err)
	}

	file, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %v", err)
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}

	err = os.Chmod(targetPath, 0755)
	if err != nil {
		return fmt.Errorf("failed to set permissions: %v", err)
	}

	return nil
}

// downloadBinary downloads a binary file
func (pm *PackageManager) downloadBinary(url, targetPath string, isPrivate bool) error {
	fmt.Printf("Downloading %s...\n", url)
	
	var resp *http.Response
	var err error
	
	if isPrivate {
		if pm.githubToken == "" {
			return fmt.Errorf("private repository requires GLITCH_DEPS_GITHUB_PAT")
		}
		
		req, err := pm.createAuthenticatedRequest("GET", url)
		if err != nil {
			return fmt.Errorf("failed to create request: %v", err)
		}
		
		client := &http.Client{}
		resp, err = client.Do(req)
	} else {
		resp, err = http.Get(url)
	}
	
	if err != nil {
		return fmt.Errorf("failed to download file: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	err = os.MkdirAll(filepath.Dir(targetPath), 0755)
	if err != nil {
		return fmt.Errorf("failed to create directory: %v", err)
	}

	file, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %v", err)
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}

	err = os.Chmod(targetPath, 0755)
	if err != nil {
		return fmt.Errorf("failed to set permissions: %v", err)
	}

	return nil
}

// buildAuthenticatedGitURL creates URL for git with token for private repositories
func (pm *PackageManager) buildAuthenticatedGitURL(source string, isPrivate bool) string {
	if !isPrivate || pm.githubToken == "" {
		return source
	}
	
	// Convert https://github.com/owner/repo.git to https://token@github.com/owner/repo.git
	if strings.HasPrefix(source, "https://github.com/") {
		return strings.Replace(source, "https://github.com/", "https://"+pm.githubToken+"@github.com/", 1)
	}
	
	return source
}

// getLatestCommitHash gets the latest commit from Git repository
func (pm *PackageManager) getLatestCommitHash(source string, isPrivate bool) (string, error) {
	gitURL := pm.buildAuthenticatedGitURL(source, isPrivate)
	
	cmd := exec.Command("git", "ls-remote", gitURL, "HEAD")
	output, err := cmd.Output()
	if err != nil {
		if isPrivate && pm.githubToken == "" {
			return "", fmt.Errorf("private repository requires GLITCH_DEPS_GITHUB_PAT")
		}
		return "", fmt.Errorf("failed to get latest commit for %s: %v", source, err)
	}

	lines := strings.Split(string(output), "\n")
	if len(lines) > 0 && len(lines[0]) > 0 {
		parts := strings.Fields(lines[0])
		if len(parts) > 0 {
			return parts[0][:8], nil // Return short hash
		}
	}
	return "", fmt.Errorf("failed to parse git ls-remote output")
}

// cloneOrUpdateRepo clones or updates repository
func (pm *PackageManager) cloneOrUpdateRepo(source, targetPath string, isPrivate bool) error {
	gitURL := pm.buildAuthenticatedGitURL(source, isPrivate)
	
	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		// Clone repository
		fmt.Printf("Cloning %s to %s...\n", source, targetPath)
		cmd := exec.Command("git", "clone", gitURL, targetPath)
		return cmd.Run()
	} else {
		// Update existing repository
		fmt.Printf("Updating %s...\n", targetPath)
		cmd := exec.Command("git", "-C", targetPath, "pull", "origin", "main")
		err := cmd.Run()
		if err != nil {
			// Try master if main didn't work
			cmd = exec.Command("git", "-C", targetPath, "pull", "origin", "master")
			return cmd.Run()
		}
		return err
	}
}

// determineDependencyType determines dependency type by name
func (pm *PackageManager) determineDependencyType(name string) string {
	// If name contains "provider", then it's a binary dependency
	if strings.Contains(strings.ToLower(name), "provider") {
		return "binary"
	}
	return "repository"
}

// installDependency installs one dependency
func (pm *PackageManager) installDependency(dep Dependency) (LockDependency, error) {
	fmt.Printf("Installing dependency: %s\n", dep.Name)

	// Determine dependency type
	depType := dep.Type
	if depType == "" {
		depType = pm.determineDependencyType(dep.Name)
	}

	targetPath := filepath.Join(pm.workDir, dep.Path)

	if depType == "binary" {
		// Download binary file from GitHub releases
		owner, repo, err := pm.extractRepoInfo(dep.Source)
		if err != nil {
			return LockDependency{}, fmt.Errorf("failed to parse repository URL: %v", err)
		}

		release, err := pm.getLatestRelease(owner, repo, dep.Private)
		if err != nil {
			return LockDependency{}, fmt.Errorf("failed to get release info: %v", err)
		}

		// Output all available assets for debugging
		fmt.Printf("Available assets in release %s:\n", release.TagName)
		for i, asset := range release.Assets {
			fmt.Printf("  [%d] %s -> %s\n", i, asset.Name, asset.BrowserDownloadURL)
		}

		// Find suitable asset for specified suffix or any asset if suffix is empty
		assetSuffix := pm.getAssetSuffixFromDep(dep)
		
		var downloadURL string
		var assetID int
		
		if assetSuffix != "" {
			// Search for specific asset suffix
			assetSuffixParts := strings.Split(assetSuffix, "_")
			if len(assetSuffixParts) != 2 {
				return LockDependency{}, fmt.Errorf("invalid asset_suffix format: %s (expected format: os_arch)", assetSuffix)
			}
			goos, goarch := assetSuffixParts[0], assetSuffixParts[1]
			
			for _, asset := range release.Assets {
				if strings.Contains(asset.Name, assetSuffix) || 
				   (strings.Contains(asset.Name, goos) && strings.Contains(asset.Name, goarch)) {
					downloadURL = asset.BrowserDownloadURL
					assetID = asset.ID
					break
				}
			}
			
			if downloadURL == "" {
				return LockDependency{}, fmt.Errorf("no suitable asset found for %s in release %s", assetSuffix, release.TagName)
			}
		} else {
			// No specific suffix - take the first available asset
			if len(release.Assets) == 0 {
				return LockDependency{}, fmt.Errorf("no assets found in release %s", release.TagName)
			}
			
			// Take the first asset
			asset := release.Assets[0]
			downloadURL = asset.BrowserDownloadURL
			assetID = asset.ID
			fmt.Printf("No asset_suffix specified, using first available asset: %s\n", asset.Name)
		}

		// For private repositories, use API, for public - direct links
		if dep.Private {
			err = pm.downloadAssetViaAPI(owner, repo, assetID, targetPath, dep.Private)
		} else {
			err = pm.downloadBinary(downloadURL, targetPath, dep.Private)
		}
		if err != nil {
			return LockDependency{}, fmt.Errorf("failed to download binary: %v", err)
		}

		lockDep := LockDependency{
			Name:      dep.Name,
			Path:      dep.Path,
			Source:    dep.Source,
			Version:   release.TagName,
			Hash:      release.TagName,
			UpdatedAt: time.Now(),
			Type:      "binary",
			Private:   dep.Private,
		}

		fmt.Printf("✓ Installed: %s (version: %s)\n", dep.Name, release.TagName)
		return lockDep, nil

	} else {
		// Clone repository as before
		err := pm.cloneOrUpdateRepo(dep.Source, targetPath, dep.Private)
		if err != nil {
			return LockDependency{}, fmt.Errorf("failed to install %s: %v", dep.Name, err)
		}

		// Get latest commit hash
		hash, err := pm.getLatestCommitHash(dep.Source, dep.Private)
		if err != nil {
			hash = "unknown"
		}

		lockDep := LockDependency{
			Name:      dep.Name,
			Path:      dep.Path,
			Source:    dep.Source,
			Version:   hash,
			Hash:      hash,
			UpdatedAt: time.Now(),
			Type:      "repository",
			Private:   dep.Private,
		}

		fmt.Printf("✓ Installed: %s (version: %s)\n", dep.Name, hash)
		return lockDep, nil
	}
}

// Install command to install dependencies
func (pm *PackageManager) Install() error {
	fmt.Println("🚀 Starting dependency installation...")

	// Load dependencies file
	deps, err := pm.loadDepsFile()
	if err != nil {
		return fmt.Errorf("failed to load %s: %v", DepsFileName, err)
	}

	// Load lock file
	lock, err := pm.loadLockFile()
	if err != nil {
		return fmt.Errorf("failed to load %s: %v", LockFileName, err)
	}

	newLock := make(LockFile)
	hasUpdates := false

	// Install each dependency
	for name, dep := range deps {
		lockDep, err := pm.installDependency(dep)
		if err != nil {
			fmt.Printf("❌ Installation error for %s: %v\n", name, err)
			continue
		}

		// Check for updates
		if oldLock, exists := lock[name]; exists {
			if oldLock.Hash != lockDep.Hash {
				fmt.Printf("📦 Update available for %s: %s -> %s\n", name, oldLock.Hash, lockDep.Hash)
				hasUpdates = true
			}
		} else {
			hasUpdates = true
		}

		newLock[name] = lockDep
	}

	// Save lock file
	err = pm.saveLockFile(newLock)
	if err != nil {
		return fmt.Errorf("failed to save %s: %v", LockFileName, err)
	}

	if hasUpdates {
		fmt.Println("📋 Updates available! Run 'glitch_deps update' to update.")
	}

	fmt.Println("✅ Installation completed!")
	return nil
}

// Update command to update dependencies
func (pm *PackageManager) Update(dependencyName, version string) error {
	fmt.Println("🔄 Starting dependency update...")

	// Load dependencies file
	deps, err := pm.loadDepsFile()
	if err != nil {
		return fmt.Errorf("failed to load %s: %v", DepsFileName, err)
	}

	// Load lock file
	lock, err := pm.loadLockFile()
	if err != nil {
		return fmt.Errorf("failed to load %s: %v", LockFileName, err)
	}

	// If specific dependency is specified
	if dependencyName != "" {
		dep, exists := deps[dependencyName]
		if !exists {
			return fmt.Errorf("dependency %s not found", dependencyName)
		}

		fmt.Printf("Updating %s...\n", dependencyName)
		lockDep, err := pm.installDependency(dep)
		if err != nil {
			return fmt.Errorf("failed to update %s: %v", dependencyName, err)
		}

		// If specific version is specified
		if version != "" {
			lockDep.Version = version
			lockDep.Hash = version
		}

		lock[dependencyName] = lockDep
	} else {
		// Update all dependencies
		for name, dep := range deps {
			fmt.Printf("Updating %s...\n", name)
			lockDep, err := pm.installDependency(dep)
			if err != nil {
				fmt.Printf("❌ Update error for %s: %v\n", name, err)
				continue
			}
			lock[name] = lockDep
		}
	}

	// Save updated lock file
	err = pm.saveLockFile(lock)
	if err != nil {
		return fmt.Errorf("failed to save %s: %v", LockFileName, err)
	}

	fmt.Println("✅ Update completed!")
	return nil
}

// SelfUpdate updates the glitch_deps binary to the latest version
func (pm *PackageManager) SelfUpdate() error {
	fmt.Println("🔄 Checking for glitch_deps updates...")
	
	const repoOwner = "glitch-vpn"
	const repoName = "glitch-deps"
	
	// Get latest release
	release, err := pm.getLatestRelease(repoOwner, repoName, false)
	if err != nil {
		return fmt.Errorf("failed to get latest release: %v", err)
	}
	
	fmt.Printf("Latest version: %s\n", release.TagName)
	
	// Find suitable asset for current platform
	var downloadURL string
	var assetName string
	
	// Determine current platform
	platform := getDefaultPlatform()
	platformParts := strings.Split(platform, "_")
	if len(platformParts) != 2 {
		return fmt.Errorf("invalid platform format: %s", platform)
	}
	goos, goarch := platformParts[0], platformParts[1]
	
	for _, asset := range release.Assets {
		if strings.Contains(asset.Name, platform) || 
		   (strings.Contains(asset.Name, goos) && strings.Contains(asset.Name, goarch)) {
			downloadURL = asset.BrowserDownloadURL
			assetName = asset.Name
			break
		}
	}
	
	if downloadURL == "" {
		return fmt.Errorf("no suitable binary found for %s", platform)
	}
	
	fmt.Printf("Downloading %s...\n", assetName)
	
	// Get current executable path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %v", err)
	}
	
	// Create temporary file
	tempPath := execPath + ".tmp"
	
	// Download new binary
	err = pm.downloadBinary(downloadURL, tempPath, false)
	if err != nil {
		return fmt.Errorf("failed to download update: %v", err)
	}
	
	// Replace current binary
	err = os.Rename(tempPath, execPath)
	if err != nil {
		// Clean up temp file
		os.Remove(tempPath)
		return fmt.Errorf("failed to replace binary: %v", err)
	}
	
	fmt.Printf("✅ Successfully updated to %s\n", release.TagName)
	return nil
}

// printUsage outputs usage help
func printUsage() {
	fmt.Println("Glitch Dependencies Manager")
	fmt.Println("Usage:")
	fmt.Println("  glitch_deps install                    - install dependencies")
	fmt.Println("  glitch_deps update                     - update all dependencies")
	fmt.Println("  glitch_deps update <dependency>        - update specific dependency")
	fmt.Println("  glitch_deps update <dependency> <version> - update to specific version")
	fmt.Println("  glitch_deps self-update                - update glitch_deps to latest version")
	fmt.Println("  glitch_deps help                       - show this help")
	fmt.Println("")
	fmt.Println("Environment variables:")
	fmt.Println("  GLITCH_DEPS_GITHUB_PAT                 - GitHub Personal Access Token for private repositories")
}

// getDefaultPlatform returns the current platform in format "os_arch"
func getDefaultPlatform() string {
	return fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)
}

// getAssetSuffixFromDep returns asset suffix from dependency or empty string
func (pm *PackageManager) getAssetSuffixFromDep(dep Dependency) string {
	return dep.AssetSuffix
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		return
	}

	pm := NewPackageManager()
	command := os.Args[1]

	switch command {
	case "install":
		err := pm.Install()
		if err != nil {
			log.Fatal("Installation error:", err)
		}

	case "update":
		var dependencyName, version string
		if len(os.Args) > 2 {
			dependencyName = os.Args[2]
		}
		if len(os.Args) > 3 {
			version = os.Args[3]
		}

		err := pm.Update(dependencyName, version)
		if err != nil {
			log.Fatal("Update error:", err)
		}

	case "self-update":
		err := pm.SelfUpdate()
		if err != nil {
			log.Fatal("Self-update error:", err)
		}

	case "help":
		printUsage()

	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}
