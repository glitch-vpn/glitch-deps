# Glitch Dependencies Manager

A lightweight package manager for managing Git repository dependencies and GitHub release binaries.

## Installation

Download the latest release from [GitHub Releases](https://github.com/glitch-vpn/glitch-deps/releases) or build from source:

```bash
go build -o glitch_deps main.go
```

## Quick Start

1. Create a `GLITCH_DEPS.json` file in your project root:

```json
{
  "my_provider": {
    "name": "my_provider",
    "path": "bin/my_provider",
    "source": "https://github.com/owner/repo.git",
    "type": "binary",
    "asset_suffix": "linux_amd64"
  },
  "my_library": {
    "name": "my_library", 
    "path": "libs/my_library",
    "source": "https://github.com/owner/library.git",
    "type": "repository"
  },
  "private_tool": {
    "name": "private_tool",
    "path": "tools/private_tool", 
    "source": "https://github.com/private-org/tool.git",
    "type": "binary",
    "asset_suffix": "linux_amd64",
    "private": true
  }
}
```

2. Install dependencies:

```bash
./glitch_deps install
```

## Configuration

### Dependency Types

- **`binary`**: Downloads binary files from GitHub releases
- **`repository`**: Clones Git repositories

### Asset Suffix Specification

For binary dependencies, you can specify the target asset suffix using the `asset_suffix` field:

```json
{
  "cross_platform_tool": {
    "name": "cross_platform_tool",
    "path": "bin/tool",
    "source": "https://github.com/owner/tool.git",
    "type": "binary",
    "asset_suffix": "windows_amd64"
  }
}
```

**Common suffixes**: `linux_amd64`, `linux_arm64`, `windows_amd64`, `darwin_amd64`, `darwin_arm64`

If `asset_suffix` is not specified, the first available asset from the release will be downloaded.

### Private Repositories

Set the `GLITCH_DEPS_GITHUB_PAT` environment variable with your GitHub Personal Access Token:

```bash
export GLITCH_DEPS_GITHUB_PAT=ghp_xxxxxxxxxxxxxxxxxxxx
```

Mark private dependencies in your config:

```json
{
  "private_tool": {
    "name": "private_tool",
    "path": "tools/private_tool", 
    "source": "https://github.com/private-org/tool.git",
    "type": "binary",
    "asset_suffix": "linux_amd64",
    "private": true
  }
}
```

## Commands

```bash
# Install all dependencies
glitch_deps install

# Update all dependencies  
glitch_deps update

# Update specific dependency
glitch_deps update my_provider

# Update to specific version
glitch_deps update my_provider v1.2.0

# Update glitch_deps itself
glitch_deps self-update

# Show help
glitch_deps help
```

## How It Works

- **Binary dependencies**: Downloads assets from GitHub releases. If `asset_suffix` is specified, searches for matching assets; otherwise downloads the first available asset
- **Repository dependencies**: Clones or pulls the latest changes from Git repositories  
- **Version tracking**: Creates `GLITCH_DEPS-lock.json` to track installed versions and hashes
- **Smart updates**: Detects when updates are available and notifies you

## Requirements

- Git (must be in PATH)
- Internet access for GitHub API
- GitHub Personal Access Token (for private repositories only)

## License

Apache 2.0 