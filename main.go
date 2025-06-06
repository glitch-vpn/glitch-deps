package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ulikunitz/xz"
)

var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

type Dependency struct {
	Path           string `json:"path"`
	Source         string `json:"source"`
	Type           string `json:"type,omitempty"`
	AssetSuffix    string `json:"asset_suffix,omitempty"`
	Private        bool   `json:"private,omitempty"`
	Extract        bool   `json:"extract,omitempty"`
	Filename       string `json:"filename,omitempty"`
	AssetName      string `json:"asset_name,omitempty"`
	AssetExtension string `json:"asset_extension,omitempty"`
}
type LockDependency struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Source  string `json:"source"`
	Version string `json:"version"`
	Hash    string `json:"hash"`
	Type    string `json:"type"`
	Private bool   `json:"private,omitempty"`
	Extract bool   `json:"extract,omitempty"`
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
	DepsFileName = "fracture.json"
	LockFileName = "fracture-lock.json"
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
	githubToken := os.Getenv("FRACTURE_GITHUB_PAT")
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
	data, err := os.ReadFile(depsPath)
	if err != nil {
		return nil, err
	}

	var deps DepsFile
	err = json.Unmarshal(data, &deps)
	return deps, err
}
func (pm *PackageManager) loadLockFile() (LockFile, error) {
	lockPath := filepath.Join(pm.workDir, pm.lockPath)
	data, err := os.ReadFile(lockPath)
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
	return os.WriteFile(lockPath, data, 0644)
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
		return nil, fmt.Errorf("private repository %s/%s requires FRACTURE_GITHUB_PAT", owner, repo)
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
			return fmt.Errorf("private repository requires FRACTURE_GITHUB_PAT")
		}

		req, err := pm.createAuthenticatedRequest("GET", url)
		if err != nil {
			return fmt.Errorf("failed to create request: %v", err)
		}

		client := &http.Client{}
		resp, _ = client.Do(req)
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
	} else if strings.HasSuffix(archivePath, ".zip") {
		return pm.extractZip(archivePath, targetDir)
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
func (pm *PackageManager) extractZip(archivePath, targetDir string) error {
	fmt.Printf("Extracting ZIP archive %s to %s...\n", archivePath, targetDir)

	zipReader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open ZIP archive: %v", err)
	}
	defer zipReader.Close()

	for _, file := range zipReader.File {
		targetPath := filepath.Join(targetDir, file.Name)
		if !strings.HasPrefix(targetPath, filepath.Clean(targetDir)+string(os.PathSeparator)) {
			return fmt.Errorf("invalid file path: %s", file.Name)
		}

		if file.FileInfo().IsDir() {
			err = os.MkdirAll(targetPath, file.Mode())
			if err != nil {
				return fmt.Errorf("failed to create directory %s: %v", targetPath, err)
			}
			continue
		}

		err = os.MkdirAll(filepath.Dir(targetPath), 0755)
		if err != nil {
			return fmt.Errorf("failed to create parent directory for %s: %v", targetPath, err)
		}

		fileReader, err := file.Open()
		if err != nil {
			return fmt.Errorf("failed to open file %s: %v", file.Name, err)
		}
		defer fileReader.Close()

		targetFile, err := os.Create(targetPath)
		if err != nil {
			return fmt.Errorf("failed to create file %s: %v", targetPath, err)
		}
		defer targetFile.Close()

		_, err = io.Copy(targetFile, fileReader)
		if err != nil {
			return fmt.Errorf("failed to write file %s: %v", targetPath, err)
		}

		err = targetFile.Chmod(file.Mode())
		if err != nil {
			return fmt.Errorf("failed to set permissions for %s: %v", targetPath, err)
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
			return "", fmt.Errorf("private repository requires FRACTURE_GITHUB_PAT")
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
	if strings.Contains(strings.ToLower(name), "source") {
		return "source"
	}
	return "repository"
}
func (pm *PackageManager) installDependency(depName string, dep Dependency) (LockDependency, error) {
	fmt.Printf("Installing dependency: %s\n", depName)
	depType := dep.Type
	if depType == "" {
		depType = pm.determineDependencyType(depName)
	}

	if depType == "source" {
		if dep.AssetName != "" {
			return LockDependency{}, fmt.Errorf("asset_name is not allowed for source type dependencies")
		}
		if dep.AssetSuffix != "" {
			return LockDependency{}, fmt.Errorf("asset_suffix is not allowed for source type dependencies")
		}
		if dep.Extract && dep.Filename != "" {
			return LockDependency{}, fmt.Errorf("filename cannot be used with extract=true for source type dependencies")
		}
		if dep.AssetExtension != "" && dep.AssetExtension != "zip" && dep.AssetExtension != "tar.gz" {
			return LockDependency{}, fmt.Errorf("asset_extension for source type must be 'zip' or 'tar.gz', got '%s'", dep.AssetExtension)
		}
		if dep.Extract && (strings.Contains(dep.Path, "@ASSET_EXTENSION") || strings.Contains(dep.Filename, "@ASSET_EXTENSION")) {
			return LockDependency{}, fmt.Errorf("@ASSET_EXTENSION placeholder cannot be used with extract=true")
		}
	}

	if depType == "source" {
		owner, repo, err := pm.extractRepoInfo(dep.Source)
		if err != nil {
			return LockDependency{}, fmt.Errorf("failed to parse repository URL: %v", err)
		}

		release, err := pm.getLatestRelease(owner, repo, dep.Private)
		if err != nil {
			return LockDependency{}, fmt.Errorf("failed to get release info: %v", err)
		}

		sourceFormat := "tar.gz"
		if dep.AssetExtension != "" {
			sourceFormat = dep.AssetExtension
		}

		expandedPath := pm.expandPathWithOptions(dep.Path, release.TagName, sourceFormat, dep.Extract)
		fmt.Printf("Original path: %s\n", dep.Path)
		fmt.Printf("Expanded path: %s\n", expandedPath)

		targetPath := filepath.Join(pm.workDir, expandedPath)

		var downloadURL string
		if sourceFormat == "zip" {
			downloadURL = fmt.Sprintf("https://github.com/%s/%s/archive/refs/tags/%s.zip", owner, repo, release.TagName)
		} else {
			downloadURL = fmt.Sprintf("https://github.com/%s/%s/archive/refs/tags/%s.tar.gz", owner, repo, release.TagName)
		}

		fmt.Printf("Downloading source code (%s) from: %s\n", sourceFormat, downloadURL)

		var actualTargetPath string
		var archiveName string

		if dep.Filename != "" {
			archiveName = pm.expandPathWithOptions(dep.Filename, release.TagName, sourceFormat, dep.Extract)
		} else {
			archiveName = fmt.Sprintf("%s-%s.%s", repo, release.TagName, sourceFormat)
		}

		if dep.Extract {
			tmpDir := filepath.Join(pm.workDir, "tmp")
			err := os.MkdirAll(tmpDir, 0755)
			if err != nil {
				return LockDependency{}, fmt.Errorf("failed to create tmp directory: %v", err)
			}
			actualTargetPath = filepath.Join(tmpDir, archiveName)
		} else {
			actualTargetPath = filepath.Join(targetPath, archiveName)
		}

		err = pm.downloadBinary(downloadURL, actualTargetPath, dep.Private)
		if err != nil {
			return LockDependency{}, fmt.Errorf("failed to download source code: %v", err)
		}

		if dep.Extract {
			tmpExtractDir := filepath.Join(pm.workDir, "tmp", "extract_"+depName)

			err = pm.extractArchive(actualTargetPath, tmpExtractDir)
			if err != nil {
				return LockDependency{}, fmt.Errorf("failed to extract source archive: %v", err)
			}

			targetDir := filepath.Join(pm.workDir, expandedPath)
			err = os.MkdirAll(targetDir, 0755)
			if err != nil {
				return LockDependency{}, fmt.Errorf("failed to create target directory: %v", err)
			}

			entries, err := os.ReadDir(tmpExtractDir)
			if err != nil {
				return LockDependency{}, fmt.Errorf("failed to read extracted directory: %v", err)
			}

			if len(entries) == 1 && entries[0].IsDir() {
				extractedDir := filepath.Join(tmpExtractDir, entries[0].Name())
				err = filepath.Walk(extractedDir, func(path string, info os.FileInfo, err error) error {
					if err != nil {
						return err
					}

					relPath, err := filepath.Rel(extractedDir, path)
					if err != nil {
						return err
					}

					if relPath == "." {
						return nil
					}

					targetPath := filepath.Join(targetDir, relPath)

					if info.IsDir() {
						return os.MkdirAll(targetPath, info.Mode())
					} else {
						targetFileDir := filepath.Dir(targetPath)
						err = os.MkdirAll(targetFileDir, 0755)
						if err != nil {
							return err
						}
						return os.Rename(path, targetPath)
					}
				})
				if err != nil {
					return LockDependency{}, fmt.Errorf("failed to move extracted files: %v", err)
				}
			} else {
				for _, entry := range entries {
					srcPath := filepath.Join(tmpExtractDir, entry.Name())
					dstPath := filepath.Join(targetDir, entry.Name())
					err = os.Rename(srcPath, dstPath)
					if err != nil {
						return LockDependency{}, fmt.Errorf("failed to move extracted file %s: %v", entry.Name(), err)
					}
				}
			}

			fmt.Printf("Extracted source code to directory: %s\n", targetDir)

			os.RemoveAll(tmpExtractDir)
			err = os.Remove(actualTargetPath)
			if err != nil {
				fmt.Printf("Warning: failed to remove archive file %s: %v\n", actualTargetPath, err)
			}
		}

		lockDep := LockDependency{
			Name:    depName,
			Path:    expandedPath,
			Source:  dep.Source,
			Version: release.TagName,
			Hash:    release.TagName,
			Type:    "source",
			Private: dep.Private,
			Extract: dep.Extract,
		}

		fmt.Printf("✓ Installed: %s (version: %s, format: %s)\n", depName, release.TagName, sourceFormat)
		return lockDep, nil

	} else if depType == "binary" {
		owner, repo, err := pm.extractRepoInfo(dep.Source)
		if err != nil {
			return LockDependency{}, fmt.Errorf("failed to parse repository URL: %v", err)
		}

		release, err := pm.getLatestRelease(owner, repo, dep.Private)
		if err != nil {
			return LockDependency{}, fmt.Errorf("failed to get release info: %v", err)
		}

		expandedPath := pm.expandPath(dep.Path, release.TagName)
		fmt.Printf("Original path: %s\n", dep.Path)
		fmt.Printf("Expanded path: %s\n", expandedPath)

		targetPath := filepath.Join(pm.workDir, expandedPath)

		fmt.Printf("Available assets in release %s:\n", release.TagName)
		for i, asset := range release.Assets {
			fmt.Printf("  [%d] %s -> %s\n", i, asset.Name, asset.BrowserDownloadURL)
		}

		var downloadURL string
		var assetID int
		var assetName string

		var candidateAssets []struct {
			ID                 int    `json:"id"`
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		}

		if dep.AssetName != "" {
			fmt.Printf("Filtering assets by asset_name: %s\n", dep.AssetName)
			for _, asset := range release.Assets {
				if strings.Contains(asset.Name, dep.AssetName) {
					candidateAssets = append(candidateAssets, asset)
				}
			}
			if len(candidateAssets) == 0 {
				return LockDependency{}, fmt.Errorf("no assets found containing asset_name '%s' in release %s", dep.AssetName, release.TagName)
			}
			fmt.Printf("Found %d assets matching asset_name '%s'\n", len(candidateAssets), dep.AssetName)
		} else {
			candidateAssets = release.Assets
		}

		if dep.AssetExtension != "" {
			fmt.Printf("Filtering assets by asset_extension: %s\n", dep.AssetExtension)
			var extensionFilteredAssets []struct {
				ID                 int    `json:"id"`
				Name               string `json:"name"`
				BrowserDownloadURL string `json:"browser_download_url"`
			}

			extension := dep.AssetExtension
			if !strings.HasPrefix(extension, ".") {
				extension = "." + extension
			}

			for _, asset := range candidateAssets {
				if strings.HasSuffix(asset.Name, extension) {
					extensionFilteredAssets = append(extensionFilteredAssets, asset)
				}
			}

			if len(extensionFilteredAssets) == 0 {
				return LockDependency{}, fmt.Errorf("no assets found with asset_extension '%s' in release %s", dep.AssetExtension, release.TagName)
			}

			candidateAssets = extensionFilteredAssets
			fmt.Printf("Found %d assets matching asset_extension '%s'\n", len(candidateAssets), dep.AssetExtension)
		}

		assetSuffix := pm.getAssetSuffixFromDep(dep)
		if assetSuffix != "" {
			var matchingAssets []struct {
				ID                 int    `json:"id"`
				Name               string `json:"name"`
				BrowserDownloadURL string `json:"browser_download_url"`
			}

			for _, asset := range candidateAssets {
				if strings.Contains(asset.Name, assetSuffix) {
					matchingAssets = append(matchingAssets, asset)
				}
			}

			if len(matchingAssets) == 0 {
				return LockDependency{}, fmt.Errorf("no assets found matching asset_suffix '%s' in release %s", assetSuffix, release.TagName)
			}

			if len(matchingAssets) > 1 {
				var assetNames []string
				for _, asset := range matchingAssets {
					assetNames = append(assetNames, asset.Name)
				}
				return LockDependency{}, fmt.Errorf("multiple assets found matching criteria. Found %d assets: %v. Please refine asset_name, asset_extension, or asset_suffix to match exactly one asset", len(matchingAssets), assetNames)
			}

			asset := matchingAssets[0]
			downloadURL = asset.BrowserDownloadURL
			assetID = asset.ID
			assetName = asset.Name
			fmt.Printf("Found matching asset: %s\n", asset.Name)
		} else {

			return LockDependency{}, fmt.Errorf("asset_suffix is required for binary dependencies. Available assets: %v", func() []string {
				var names []string
				for _, asset := range candidateAssets {
					names = append(names, asset.Name)
				}
				return names
			}())
		}

		var actualTargetPath string
		if dep.Extract && (strings.HasSuffix(assetName, ".tar.gz") || strings.HasSuffix(assetName, ".tar.xz") || strings.HasSuffix(assetName, ".zip")) {
			tmpDir := filepath.Join(pm.workDir, "tmp")
			err := os.MkdirAll(tmpDir, 0755)
			if err != nil {
				return LockDependency{}, fmt.Errorf("failed to create tmp directory: %v", err)
			}
			actualTargetPath = filepath.Join(tmpDir, assetName)
		} else {

			actualTargetPath = filepath.Join(targetPath, assetName)
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
			if strings.HasSuffix(assetName, ".tar.gz") || strings.HasSuffix(assetName, ".tar.xz") || strings.HasSuffix(assetName, ".zip") {
				tmpExtractDir := filepath.Join(pm.workDir, "tmp", "extract_"+depName)

				err = pm.extractArchive(actualTargetPath, tmpExtractDir)
				if err != nil {
					return LockDependency{}, fmt.Errorf("failed to extract archive: %v", err)
				}

				var extractedFiles []string
				err = filepath.Walk(tmpExtractDir, func(path string, info os.FileInfo, err error) error {
					if err != nil {
						return err
					}
					if !info.IsDir() && path != tmpExtractDir {
						extractedFiles = append(extractedFiles, path)
					}
					return nil
				})
				if err != nil {
					return LockDependency{}, fmt.Errorf("failed to walk extracted files: %v", err)
				}

				fmt.Printf("Found %d files in archive\n", len(extractedFiles))

				if dep.Filename != "" {
					if len(extractedFiles) > 1 {
						return LockDependency{}, fmt.Errorf("filename specified but archive contains %d files (expected 1). Remove filename to extract all files to directory", len(extractedFiles))
					}
					if len(extractedFiles) == 0 {
						return LockDependency{}, fmt.Errorf("no files found in archive")
					}

					targetDir := filepath.Join(pm.workDir, expandedPath)
					err = os.MkdirAll(targetDir, 0755)
					if err != nil {
						return LockDependency{}, fmt.Errorf("failed to create target directory: %v", err)
					}

					finalPath := filepath.Join(targetDir, dep.Filename)
					err = os.Rename(extractedFiles[0], finalPath)
					if err != nil {
						return LockDependency{}, fmt.Errorf("failed to move extracted file: %v", err)
					}
					fmt.Printf("Extracted single file as: %s\n", finalPath)
				} else {
					targetDir := filepath.Join(pm.workDir, expandedPath)
					err = os.MkdirAll(targetDir, 0755)
					if err != nil {
						return LockDependency{}, fmt.Errorf("failed to create target directory: %v", err)
					}

					for _, file := range extractedFiles {
						relPath, err := filepath.Rel(tmpExtractDir, file)
						if err != nil {
							return LockDependency{}, fmt.Errorf("failed to get relative path for %s: %v", file, err)
						}

						finalPath := filepath.Join(targetDir, relPath)
						finalDir := filepath.Dir(finalPath)

						err = os.MkdirAll(finalDir, 0755)
						if err != nil {
							return LockDependency{}, fmt.Errorf("failed to create directory %s: %v", finalDir, err)
						}

						err = os.Rename(file, finalPath)
						if err != nil {
							return LockDependency{}, fmt.Errorf("failed to move extracted file %s: %v", relPath, err)
						}
					}
					fmt.Printf("Extracted %d files to directory: %s\n", len(extractedFiles), targetDir)
				}

				os.RemoveAll(tmpExtractDir)
				err = os.Remove(actualTargetPath)
				if err != nil {
					fmt.Printf("Warning: failed to remove archive file %s: %v\n", actualTargetPath, err)
				}
			} else {
				fmt.Printf("Warning: extract flag is set but %s is not a supported archive format\n", assetName)
			}
		}

		lockDep := LockDependency{
			Name:    depName,
			Path:    expandedPath,
			Source:  dep.Source,
			Version: release.TagName,
			Hash:    release.TagName,
			Type:    "binary",
			Private: dep.Private,
			Extract: dep.Extract,
		}

		fmt.Printf("✓ Installed: %s (version: %s)\n", depName, release.TagName)
		return lockDep, nil

	} else {
		hash, err := pm.getLatestCommitHash(dep.Source, dep.Private)
		if err != nil {
			hash = "unknown"
		}

		expandedPath := pm.expandPathWithOptions(dep.Path, hash, "", dep.Extract)
		fmt.Printf("Original path: %s\n", dep.Path)
		fmt.Printf("Expanded path: %s\n", expandedPath)

		targetPath := filepath.Join(pm.workDir, expandedPath)

		err = pm.cloneOrUpdateRepo(dep.Source, targetPath, dep.Private)
		if err != nil {
			return LockDependency{}, fmt.Errorf("failed to install %s: %v", depName, err)
		}

		lockDep := LockDependency{
			Name:    depName,
			Path:    expandedPath,
			Source:  dep.Source,
			Version: hash,
			Hash:    hash,
			Type:    "repository",
			Private: dep.Private,
			Extract: dep.Extract,
		}

		fmt.Printf("✓ Installed: %s (version: %s)\n", depName, hash)
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
		lockDep, err := pm.installDependency(name, dep)
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
		fmt.Println("📋 Updates available! Run 'fracture update' to update.")
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
		lockDep, err := pm.installDependency(dependencyName, dep)
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
			lockDep, err := pm.installDependency(name, dep)
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
	fmt.Println("🔄 Checking for fracture updates...")

	const repoOwner = "glitch-vpn"
	const repoName = "fracture"
	release, err := pm.getLatestRelease(repoOwner, repoName, false)
	if err != nil {
		return fmt.Errorf("failed to get latest release: %v", err)
	}

	fmt.Printf("Latest version: %s\n", release.TagName)

	currentOS := runtime.GOOS
	currentArch := runtime.GOARCH

	fmt.Printf("Current platform: %s/%s\n", currentOS, currentArch)
	fmt.Printf("Available assets:\n")
	for i, asset := range release.Assets {
		fmt.Printf("  [%d] %s\n", i, asset.Name)
	}

	var downloadURL string
	var assetName string
	bestMatch := pm.findBestAssetMatch(release.Assets, currentOS, currentArch)
	if bestMatch != nil {
		downloadURL = bestMatch.BrowserDownloadURL
		assetName = bestMatch.Name
		fmt.Printf("Selected asset: %s\n", assetName)
	}

	if downloadURL == "" {
		return fmt.Errorf("no suitable binary found for %s/%s", currentOS, currentArch)
	}

	fmt.Printf("Downloading %s...\n", assetName)
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %v", err)
	}

	tmpDir := filepath.Join(filepath.Dir(execPath), "tmp_update")
	err = os.MkdirAll(tmpDir, 0755)
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	downloadPath := filepath.Join(tmpDir, assetName)
	err = pm.downloadBinary(downloadURL, downloadPath, false)
	if err != nil {
		return fmt.Errorf("failed to download update: %v", err)
	}

	var newBinaryPath string

	if strings.HasSuffix(assetName, ".tar.gz") {
		extractDir := filepath.Join(tmpDir, "extracted")
		err = pm.extractArchive(downloadPath, extractDir)
		if err != nil {
			return fmt.Errorf("failed to extract archive: %v", err)
		}

		files, err := filepath.Glob(filepath.Join(extractDir, "*"))
		if err != nil {
			return fmt.Errorf("failed to list extracted files: %v", err)
		}

		for _, file := range files {
			if info, err := os.Stat(file); err == nil && !info.IsDir() {
				if info.Mode()&0111 != 0 || strings.Contains(filepath.Base(file), "fracture") {
					newBinaryPath = file
					break
				}
			}
		}

		if newBinaryPath == "" {
			return fmt.Errorf("no executable binary found in archive")
		}
	} else if strings.HasSuffix(assetName, ".zip") {
		extractDir := filepath.Join(tmpDir, "extracted")
		err = pm.extractArchive(downloadPath, extractDir)
		if err != nil {
			return fmt.Errorf("failed to extract ZIP archive: %v", err)
		}

		files, err := filepath.Glob(filepath.Join(extractDir, "*"))
		if err != nil {
			return fmt.Errorf("failed to list extracted files: %v", err)
		}

		for _, file := range files {
			if info, err := os.Stat(file); err == nil && !info.IsDir() {
				if info.Mode()&0111 != 0 || strings.Contains(filepath.Base(file), "fracture") {
					newBinaryPath = file
					break
				}
			}
		}

		if newBinaryPath == "" {
			return fmt.Errorf("no executable binary found in ZIP archive")
		}
	} else {
		newBinaryPath = downloadPath
	}

	err = os.Chmod(newBinaryPath, 0755)
	if err != nil {
		return fmt.Errorf("failed to set executable permissions: %v", err)
	}

	fmt.Println("Testing new binary...")
	cmd := exec.Command(newBinaryPath, "version")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("new binary failed to run: %v", err)
	}
	fmt.Printf("New binary version output:\n%s", output)

	tempExecPath := execPath + ".tmp"
	err = os.Rename(newBinaryPath, tempExecPath)
	if err != nil {
		return fmt.Errorf("failed to move new binary: %v", err)
	}

	err = os.Rename(tempExecPath, execPath)
	if err != nil {
		os.Remove(tempExecPath)
		return fmt.Errorf("failed to replace binary: %v", err)
	}

	fmt.Printf("✅ Successfully updated to %s\n", release.TagName)
	return nil
}

func (pm *PackageManager) findBestAssetMatch(assets []struct {
	ID                 int    `json:"id"`
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}, targetOS, targetArch string) *struct {
	ID                 int    `json:"id"`
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
} {
	patterns := []string{
		fmt.Sprintf("%s_%s", targetOS, targetArch),
		fmt.Sprintf("%s-%s", targetOS, targetArch),
		fmt.Sprintf("%s.%s", targetOS, targetArch),
	}

	if targetOS == "darwin" {
		patterns = append(patterns, fmt.Sprintf("macos_%s", targetArch))
		patterns = append(patterns, fmt.Sprintf("macos-%s", targetArch))
		patterns = append(patterns, fmt.Sprintf("mac_%s", targetArch))
		patterns = append(patterns, fmt.Sprintf("mac-%s", targetArch))
	}

	if targetOS == "windows" {
		patterns = append(patterns, fmt.Sprintf("win_%s", targetArch))
		patterns = append(patterns, fmt.Sprintf("win-%s", targetArch))
		patterns = append(patterns, fmt.Sprintf("win32_%s", targetArch))
		patterns = append(patterns, fmt.Sprintf("win32-%s", targetArch))
		for i, pattern := range patterns {
			patterns = append(patterns, pattern+".exe")
			patterns[i] = pattern + ".exe"
		}
	}

	archAliases := map[string][]string{
		"amd64": {"x86_64", "x64"},
		"arm64": {"aarch64"},
		"386":   {"i386", "x86"},
	}

	if aliases, exists := archAliases[targetArch]; exists {
		for _, alias := range aliases {
			patterns = append(patterns, fmt.Sprintf("%s_%s", targetOS, alias))
			patterns = append(patterns, fmt.Sprintf("%s-%s", targetOS, alias))
			if targetOS == "windows" {
				patterns = append(patterns, fmt.Sprintf("%s_%s.exe", targetOS, alias))
				patterns = append(patterns, fmt.Sprintf("%s-%s.exe", targetOS, alias))
			}
		}
	}

	for _, pattern := range patterns {
		for i := range assets {
			assetName := strings.ToLower(assets[i].Name)
			if strings.Contains(assetName, strings.ToLower(pattern)) {
				fmt.Printf("Found exact match with pattern '%s': %s\n", pattern, assets[i].Name)
				return &assets[i]
			}
		}
	}

	for i := range assets {
		assetName := strings.ToLower(assets[i].Name)
		containsOS := strings.Contains(assetName, targetOS)
		containsArch := strings.Contains(assetName, targetArch)

		if !containsArch {
			if aliases, exists := archAliases[targetArch]; exists {
				for _, alias := range aliases {
					if strings.Contains(assetName, alias) {
						containsArch = true
						break
					}
				}
			}
		}

		if !containsOS && targetOS == "darwin" {
			containsOS = strings.Contains(assetName, "macos") || strings.Contains(assetName, "mac")
		}
		if !containsOS && targetOS == "windows" {
			containsOS = strings.Contains(assetName, "win") || strings.Contains(assetName, "win32")
		}

		if containsOS && containsArch {
			fmt.Printf("Found fallback match: %s (contains %s and %s)\n", assets[i].Name, targetOS, targetArch)
			return &assets[i]
		}
	}

	return nil
}
func printUsage() {
	fmt.Println("Fracture. Dependencies Manager")
	fmt.Println("Usage:")
	fmt.Println("  fracture install [-c config.json]       - install dependencies")
	fmt.Println("  fracture update [-c config.json]        - update all dependencies")
	fmt.Println("  fracture update <dependency> [-c config.json] - update specific dependency")
	fmt.Println("  fracture update <dependency> <version> [-c config.json] - update to specific version")
	fmt.Println("  fracture self-update                    - update fracture to latest version")
	fmt.Println("  fracture version                        - show version information")
	fmt.Println("  fracture help                           - show this help")
	fmt.Println("")
	fmt.Println("Flags:")
	fmt.Println("  -c <path>                                  - path to config file (default: fracture.json)")
	fmt.Println("")
	fmt.Println("Dependency types:")
	fmt.Println("  binary     - download binary assets from GitHub releases")
	fmt.Println("  source     - download source code archives from GitHub releases")
	fmt.Println("  repository - clone Git repositories")
	fmt.Println("")
	fmt.Println("Source type configuration:")
	fmt.Println("  asset_extension - 'zip' or 'tar.gz' (default: 'tar.gz')")
	fmt.Println("  extract         - extract archive contents (default: false)")
	fmt.Println("  filename        - custom archive filename (only when extract=false)")
	fmt.Println("  Note: asset_name and asset_suffix are not allowed for source type")
	fmt.Println("")
	fmt.Println("Path substitutions:")
	fmt.Println("  @VERSION        - replaced with release tag/version")
	fmt.Println("  @TIMESTAMP      - replaced with current unix timestamp")
	fmt.Println("  @ASSET_EXTENSION - replaced with file extension (only when extract=false)")
	fmt.Println("  $ENV_VAR        - replaced with environment variable value")
	fmt.Println("")
	fmt.Println("Environment variables:")
	fmt.Println("  FRACTURE_GITHUB_PAT                     - GitHub Personal Access Token for private repositories")
}
func printVersion() {
	fmt.Printf("fracture version %s\n", Version)
	fmt.Printf("Git commit: %s\n", GitCommit)
	fmt.Printf("Build date: %s\n", BuildDate)
	fmt.Printf("Go version: %s\n", runtime.Version())
	fmt.Printf("OS/Arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
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

func (pm *PackageManager) getAssetSuffixFromDep(dep Dependency) string {
	return dep.AssetSuffix
}

func (pm *PackageManager) expandPath(path, version string) string {
	return pm.expandPathWithOptions(path, version, "", false)
}

func (pm *PackageManager) expandPathWithOptions(path, version, assetExtension string, extractMode bool) string {
	expanded := path

	if strings.Contains(expanded, "@VERSION") {
		expanded = strings.ReplaceAll(expanded, "@VERSION", version)
	}

	if strings.Contains(expanded, "@TIMESTAMP") {
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		expanded = strings.ReplaceAll(expanded, "@TIMESTAMP", timestamp)
	}

	if strings.Contains(expanded, "@ASSET_EXTENSION") {
		if extractMode {
			expanded = strings.ReplaceAll(expanded, "@ASSET_EXTENSION", "")
		} else if assetExtension != "" {
			expanded = strings.ReplaceAll(expanded, "@ASSET_EXTENSION", assetExtension)
		} else {
			expanded = strings.ReplaceAll(expanded, "@ASSET_EXTENSION", "")
		}
	}

	envVarPattern := regexp.MustCompile(`\$([A-Z_][A-Z0-9_]*)`)
	matches := envVarPattern.FindAllStringSubmatch(expanded, -1)

	for _, match := range matches {
		if len(match) >= 2 {
			envVarName := match[1]
			envVarValue := os.Getenv(envVarName)
			placeholder := "$" + envVarName
			expanded = strings.ReplaceAll(expanded, placeholder, envVarValue)
		}
	}

	return expanded
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

	case "version":
		printVersion()

	case "help":
		printUsage()

	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}
