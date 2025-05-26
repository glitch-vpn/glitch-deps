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
	"archive/tar"
	"compress/gzip"
	"github.com/ulikunitz/xz"
)
type Dependency struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Source      string `json:"source"`
	Type        string `json:"type,omitempty"`         
	AssetSuffix string `json:"asset_suffix,omitempty"` 
	Private     bool   `json:"private,omitempty"`      
	Extract     bool   `json:"extract,omitempty"`      
}
type LockDependency struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Source    string    `json:"source"`
	Version   string    `json:"version"`
	Hash      string    `json:"hash"`
	Type      string    `json:"type"`
	Private   bool      `json:"private,omitempty"`
	Extract   bool      `json:"extract,omitempty"`
}
type GitHubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		ID                 int    `json:"id"`
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}
type DepsFile map[string]Dependency
type LockFile map[string]LockDependency

const (
	DepsFileName = "GLITCH_DEPS.json"
	LockFileName = "GLITCH_DEPS-lock.json"
)
type PackageManager struct {
	workDir     string
	githubToken string
	configPath  string
	lockPath    string
}
func NewPackageManager(configPath string) *PackageManager {
	wd, err := os.Getwd()
	if err != nil {
		log.Fatal("Failed to get working directory:", err)
	}
	githubToken := os.Getenv("GLITCH_DEPS_GITHUB_PAT")
	if configPath == "" {
		configPath = DepsFileName
	}
	lockPath := generateLockFileName(configPath)
	
	return &PackageManager{
		workDir:     wd,
		githubToken: githubToken,
		configPath:  configPath,
		lockPath:    lockPath,
	}
}
func generateLockFileName(configPath string) string {
	ext := filepath.Ext(configPath)
	nameWithoutExt := strings.TrimSuffix(configPath, ext)
	return nameWithoutExt + "-lock.json"
}
func (pm *PackageManager) loadDepsFile() (DepsFile, error) {
	depsPath := filepath.Join(pm.workDir, pm.configPath)
	data, err := ioutil.ReadFile(depsPath)
	if err != nil {
		return nil, err
	}

	var deps DepsFile
	err = json.Unmarshal(data, &deps)
	return deps, err
}
func (pm *PackageManager) loadLockFile() (LockFile, error) {
	lockPath := filepath.Join(pm.workDir, pm.lockPath)
	data, err := ioutil.ReadFile(lockPath)
	if err != nil {
		return make(LockFile), nil 
	}

	var lock LockFile
	err = json.Unmarshal(data, &lock)
	if err != nil {
		return make(LockFile), nil
	}
	return lock, nil
}
func (pm *PackageManager) saveLockFile(lock LockFile) error {
	lockPath := filepath.Join(pm.workDir, pm.lockPath)
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(lockPath, data, 0644)
}
func (pm *PackageManager) extractRepoInfo(source string) (string, string, error) {
	re := regexp.MustCompile(`github\.com/([^/]+)/([^/]+)(?:\.git)?`)
	matches := re.FindStringSubmatch(source)
	if len(matches) < 3 {
		return "", "", fmt.Errorf("invalid GitHub URL format: %s", source)
	}
	return matches[1], strings.TrimSuffix(matches[2], ".git"), nil
}
func (pm *PackageManager) createAuthenticatedRequest(method, url string) (*http.Request, error) {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, err
	}
	if pm.githubToken != "" {
		req.Header.Set("Authorization", "Bearer "+pm.githubToken)
	}
	if strings.Contains(url, "releases/download") {
		req.Header.Set("Accept", "application/octet-stream")
	}
	
	return req, nil
}
func (pm *PackageManager) getLatestRelease(owner, repo string, isPrivate bool) (*GitHubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
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
func (pm *PackageManager) extractArchive(archivePath, targetDir string) error {
	fmt.Printf("Extracting archive %s to %s...\n", archivePath, targetDir)
	err := os.MkdirAll(targetDir, 0755)
	if err != nil {
		return fmt.Errorf("failed to create target directory: %v", err)
	}
	if strings.HasSuffix(archivePath, ".tar.gz") {
		return pm.extractTarGz(archivePath, targetDir)
	} else if strings.HasSuffix(archivePath, ".tar.xz") {
		return pm.extractTarXz(archivePath, targetDir)
	}
	
	return fmt.Errorf("unsupported archive format: %s", archivePath)
}
func (pm *PackageManager) extractTarGz(archivePath, targetDir string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open archive: %v", err)
	}
	defer file.Close()
	
	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %v", err)
	}
	defer gzReader.Close()
	
	tarReader := tar.NewReader(gzReader)
	
	return pm.extractTarReader(tarReader, targetDir)
}
func (pm *PackageManager) extractTarXz(archivePath, targetDir string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open archive: %v", err)
	}
	defer file.Close()
	
	xzReader, err := xz.NewReader(file)
	if err != nil {
		return fmt.Errorf("failed to create xz reader: %v", err)
	}
	
	tarReader := tar.NewReader(xzReader)
	
	return pm.extractTarReader(tarReader, targetDir)
}
func (pm *PackageManager) extractTarReader(tarReader *tar.Reader, targetDir string) error {
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar header: %v", err)
		}
		
		targetPath := filepath.Join(targetDir, header.Name)
		if !strings.HasPrefix(targetPath, filepath.Clean(targetDir)+string(os.PathSeparator)) {
			return fmt.Errorf("invalid file path: %s", header.Name)
		}
		
		switch header.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(targetPath, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("failed to create directory %s: %v", targetPath, err)
			}
		case tar.TypeReg:
			err = os.MkdirAll(filepath.Dir(targetPath), 0755)
			if err != nil {
				return fmt.Errorf("failed to create parent directory for %s: %v", targetPath, err)
			}
			
			file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("failed to create file %s: %v", targetPath, err)
			}
			
			_, err = io.Copy(file, tarReader)
			file.Close()
			if err != nil {
				return fmt.Errorf("failed to write file %s: %v", targetPath, err)
			}
		}
	}
	
	return nil
}
func (pm *PackageManager) buildAuthenticatedGitURL(source string, isPrivate bool) string {
	if !isPrivate || pm.githubToken == "" {
		return source
	}
	if strings.HasPrefix(source, "https://github.com/") {
		return strings.Replace(source, "https://github.com/", "https://"+pm.githubToken+"@github.com/", 1)
	}
	
	return source
}
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
			return parts[0][:8], nil 
		}
	}
	return "", fmt.Errorf("failed to parse git ls-remote output")
}
func (pm *PackageManager) cloneOrUpdateRepo(source, targetPath string, isPrivate bool) error {
	gitURL := pm.buildAuthenticatedGitURL(source, isPrivate)
	
	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		fmt.Printf("Cloning %s to %s...\n", source, targetPath)
		cmd := exec.Command("git", "clone", gitURL, targetPath)
		return cmd.Run()
	} else {
		fmt.Printf("Updating %s...\n", targetPath)
		cmd := exec.Command("git", "-C", targetPath, "pull", "origin", "main")
		err := cmd.Run()
		if err != nil {
			cmd = exec.Command("git", "-C", targetPath, "pull", "origin", "master")
			return cmd.Run()
		}
		return err
	}
}
func (pm *PackageManager) determineDependencyType(name string) string {
	if strings.Contains(strings.ToLower(name), "provider") {
		return "binary"
	}
	return "repository"
}
func (pm *PackageManager) installDependency(dep Dependency) (LockDependency, error) {
	fmt.Printf("Installing dependency: %s\n", dep.Name)
	depType := dep.Type
	if depType == "" {
		depType = pm.determineDependencyType(dep.Name)
	}

	targetPath := filepath.Join(pm.workDir, dep.Path)

	if depType == "binary" {
		owner, repo, err := pm.extractRepoInfo(dep.Source)
		if err != nil {
			return LockDependency{}, fmt.Errorf("failed to parse repository URL: %v", err)
		}

		release, err := pm.getLatestRelease(owner, repo, dep.Private)
		if err != nil {
			return LockDependency{}, fmt.Errorf("failed to get release info: %v", err)
		}
		fmt.Printf("Available assets in release %s:\n", release.TagName)
		for i, asset := range release.Assets {
			fmt.Printf("  [%d] %s -> %s\n", i, asset.Name, asset.BrowserDownloadURL)
		}
		assetSuffix := pm.getAssetSuffixFromDep(dep)
		
		var downloadURL string
		var assetID int
		var assetName string
		
		if assetSuffix != "" {
			var found bool
			
			for _, asset := range release.Assets {
				if strings.Contains(asset.Name, assetSuffix) {
					downloadURL = asset.BrowserDownloadURL
					assetID = asset.ID
					assetName = asset.Name
					found = true
					fmt.Printf("Found matching asset: %s\n", asset.Name)
					break
				}
			}
			
			if !found {
				return LockDependency{}, fmt.Errorf("no suitable asset found for %s in release %s", assetSuffix, release.TagName)
			}
		} else {
			if len(release.Assets) == 0 {
				return LockDependency{}, fmt.Errorf("no assets found in release %s", release.TagName)
			}
			asset := release.Assets[0]
			downloadURL = asset.BrowserDownloadURL
			assetID = asset.ID
			assetName = asset.Name
			fmt.Printf("No asset_suffix specified, using first available asset: %s\n", asset.Name)
		}
		var actualTargetPath string
		if dep.Extract && (strings.HasSuffix(assetName, ".tar.gz") || strings.HasSuffix(assetName, ".tar.xz")) {
			tmpDir := filepath.Join(pm.workDir, "tmp")
			err := os.MkdirAll(tmpDir, 0755)
			if err != nil {
				return LockDependency{}, fmt.Errorf("failed to create tmp directory: %v", err)
			}
			actualTargetPath = filepath.Join(tmpDir, assetName)
		} else {
			actualTargetPath = targetPath
		}
		if dep.Private {
			err = pm.downloadAssetViaAPI(owner, repo, assetID, actualTargetPath, dep.Private)
		} else {
			err = pm.downloadBinary(downloadURL, actualTargetPath, dep.Private)
		}
		if err != nil {
			return LockDependency{}, fmt.Errorf("failed to download binary: %v", err)
		}
		if dep.Extract {
			if strings.HasSuffix(assetName, ".tar.gz") || strings.HasSuffix(assetName, ".tar.xz") {
				var extractDir string
				
				if strings.HasSuffix(dep.Path, "/") {
					extractDir = filepath.Join(pm.workDir, dep.Path)
					
					err = pm.extractArchive(actualTargetPath, extractDir)
					if err != nil {
						return LockDependency{}, fmt.Errorf("failed to extract archive: %v", err)
					}
					
					fmt.Printf("Extracted archive to directory: %s\n", extractDir)
				} else {
					tmpExtractDir := filepath.Join(pm.workDir, "tmp", "extract_"+dep.Name)
					
					err = pm.extractArchive(actualTargetPath, tmpExtractDir)
					if err != nil {
						return LockDependency{}, fmt.Errorf("failed to extract archive: %v", err)
					}
					files, err := filepath.Glob(filepath.Join(tmpExtractDir, "*"))
					if err != nil {
						return LockDependency{}, fmt.Errorf("failed to list extracted files: %v", err)
					}
					var extractedFiles []string
					for _, file := range files {
						if info, err := os.Stat(file); err == nil && !info.IsDir() {
							extractedFiles = append(extractedFiles, file)
						}
					}
					
					finalDir := filepath.Dir(filepath.Join(pm.workDir, dep.Path))
					err = os.MkdirAll(finalDir, 0755)
					if err != nil {
						return LockDependency{}, fmt.Errorf("failed to create target directory: %v", err)
					}
					
					if len(extractedFiles) == 1 {
						finalPath := filepath.Join(pm.workDir, dep.Path)
						err = os.Rename(extractedFiles[0], finalPath)
						if err != nil {
							return LockDependency{}, fmt.Errorf("failed to move extracted file: %v", err)
						}
						fmt.Printf("Extracted single file to: %s\n", finalPath)
					} else {
						for _, file := range extractedFiles {
							fileName := filepath.Base(file)
							finalPath := filepath.Join(finalDir, fileName)
							err = os.Rename(file, finalPath)
							if err != nil {
								return LockDependency{}, fmt.Errorf("failed to move extracted file %s: %v", fileName, err)
							}
						}
						fmt.Printf("Extracted %d files to directory: %s\n", len(extractedFiles), finalDir)
					}
					os.RemoveAll(tmpExtractDir)
				}
				err = os.Remove(actualTargetPath)
				if err != nil {
					fmt.Printf("Warning: failed to remove archive file %s: %v\n", actualTargetPath, err)
				}
			} else {
				fmt.Printf("Warning: extract flag is set but %s is not a supported archive format\n", assetName)
			}
		}

		lockDep := LockDependency{
			Name:      dep.Name,
			Path:      dep.Path,
			Source:    dep.Source,
			Version:   release.TagName,
			Hash:      release.TagName,
			Type:      "binary",
			Private:   dep.Private,
			Extract:   dep.Extract,
		}

		fmt.Printf("✓ Installed: %s (version: %s)\n", dep.Name, release.TagName)
		return lockDep, nil

	} else {
		err := pm.cloneOrUpdateRepo(dep.Source, targetPath, dep.Private)
		if err != nil {
			return LockDependency{}, fmt.Errorf("failed to install %s: %v", dep.Name, err)
		}
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
			Type:      "repository",
			Private:   dep.Private,
			Extract:   dep.Extract,
		}

		fmt.Printf("✓ Installed: %s (version: %s)\n", dep.Name, hash)
		return lockDep, nil
	}
}
func (pm *PackageManager) Install() error {
	fmt.Println("🚀 Starting dependency installation...")
	deps, err := pm.loadDepsFile()
	if err != nil {
		return fmt.Errorf("failed to load %s: %v", pm.configPath, err)
	}
	lock, err := pm.loadLockFile()
	if err != nil {
		return fmt.Errorf("failed to load %s: %v", pm.lockPath, err)
	}

	newLock := make(LockFile)
	hasUpdates := false
	for name, dep := range deps {
		lockDep, err := pm.installDependency(dep)
		if err != nil {
			fmt.Printf("❌ Installation error for %s: %v\n", name, err)
			continue
		}
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
	err = pm.saveLockFile(newLock)
	if err != nil {
		return fmt.Errorf("failed to save %s: %v", pm.lockPath, err)
	}

	if hasUpdates {
		fmt.Println("📋 Updates available! Run 'glitch_deps update' to update.")
	}

	fmt.Println("✅ Installation completed!")
	return nil
}
func (pm *PackageManager) Update(dependencyName, version string) error {
	fmt.Println("🔄 Starting dependency update...")
	deps, err := pm.loadDepsFile()
	if err != nil {
		return fmt.Errorf("failed to load %s: %v", pm.configPath, err)
	}
	lock, err := pm.loadLockFile()
	if err != nil {
		return fmt.Errorf("failed to load %s: %v", pm.lockPath, err)
	}
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
		if version != "" {
			lockDep.Version = version
			lockDep.Hash = version
		}

		lock[dependencyName] = lockDep
	} else {
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
	err = pm.saveLockFile(lock)
	if err != nil {
		return fmt.Errorf("failed to save %s: %v", pm.lockPath, err)
	}

	fmt.Println("✅ Update completed!")
	return nil
}
func (pm *PackageManager) SelfUpdate() error {
	fmt.Println("🔄 Checking for glitch_deps updates...")
	
	const repoOwner = "glitch-vpn"
	const repoName = "glitch-deps"
	release, err := pm.getLatestRelease(repoOwner, repoName, false)
	if err != nil {
		return fmt.Errorf("failed to get latest release: %v", err)
	}
	
	fmt.Printf("Latest version: %s\n", release.TagName)
	var downloadURL string
	var assetName string
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
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %v", err)
	}
	tempPath := execPath + ".tmp"
	err = pm.downloadBinary(downloadURL, tempPath, false)
	if err != nil {
		return fmt.Errorf("failed to download update: %v", err)
	}
	err = os.Rename(tempPath, execPath)
	if err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to replace binary: %v", err)
	}
	
	fmt.Printf("✅ Successfully updated to %s\n", release.TagName)
	return nil
}
func printUsage() {
	fmt.Println("Glitch Dependencies Manager")
	fmt.Println("Usage:")
	fmt.Println("  glitch_deps install [-c config.json]       - install dependencies")
	fmt.Println("  glitch_deps update [-c config.json]        - update all dependencies")
	fmt.Println("  glitch_deps update <dependency> [-c config.json] - update specific dependency")
	fmt.Println("  glitch_deps update <dependency> <version> [-c config.json] - update to specific version")
	fmt.Println("  glitch_deps self-update                    - update glitch_deps to latest version")
	fmt.Println("  glitch_deps help                           - show this help")
	fmt.Println("")
	fmt.Println("Flags:")
	fmt.Println("  -c <path>                                  - path to config file (default: GLITCH_DEPS.json)")
	fmt.Println("")
	fmt.Println("Environment variables:")
	fmt.Println("  GLITCH_DEPS_GITHUB_PAT                     - GitHub Personal Access Token for private repositories")
}
func parseFlags(args []string) (string, []string) {
	var configPath string
	var remainingArgs []string
	
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" && i+1 < len(args) {
			configPath = args[i+1]
			i++ 
		} else {
			remainingArgs = append(remainingArgs, args[i])
		}
	}
	
	return configPath, remainingArgs
}
func getDefaultPlatform() string {
	return fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)
}
func (pm *PackageManager) getAssetSuffixFromDep(dep Dependency) string {
	return dep.AssetSuffix
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		return
	}
	configPath, args := parseFlags(os.Args[1:])
	
	if len(args) < 1 {
		printUsage()
		return
	}

	pm := NewPackageManager(configPath)
	command := args[0]

	switch command {
	case "install":
		err := pm.Install()
		if err != nil {
			log.Fatal("Installation error:", err)
		}

	case "update":
		var dependencyName, version string
		if len(args) > 1 {
			dependencyName = args[1]
		}
		if len(args) > 2 {
			version = args[2]
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
