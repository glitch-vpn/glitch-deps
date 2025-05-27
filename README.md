# Glitch Dependencies Manager

A lightweight package manager for managing Git repository dependencies and GitHub release binaries.

## Table of Contents

- [Installation](#installation)
- [Quick Start](#quick-start)
- [Configuration](#configuration)
  - [Custom Config Files](#custom-config-files)
  - [Dependency Types](#dependency-types)
  - [Asset Suffix Specification](#asset-suffix-specification)
  - [Archive Extraction](#archive-extraction)
  - [Source Code Dependencies](#source-code-dependencies)
  - [Private Repositories](#private-repositories)
  - [Path Variables](#path-variables)
- [Commands](#commands)
- [Use Cases](#use-cases)
  - [Multi-Environment Setup](#multi-environment-setup)
  - [Project-Specific Dependencies](#project-specific-dependencies)
- [How It Works](#how-it-works)
- [Requirements](#requirements)
- [License](#license)

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
    "path": "bin/my_provider",
    "source": "https://github.com/owner/repo.git",
    "type": "binary",
    "asset_suffix": "linux_amd64"
  },
  "my_library": {
    "path": "libs/my_library",
    "source": "https://github.com/owner/library.git",
    "type": "repository"
  },
  "source_code": {
    "path": "sources/project",
    "source": "https://github.com/owner/project.git",
    "type": "source",
    "asset_extension": "tar.gz",
    "extract": true
  },
  "private_tool": {
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

### Path Variables

You can use dynamic variables in dependency paths to create version-specific or environment-specific installations:

**Supported variables:**
- `@VERSION` - Replaced with the actual release version/tag (for binaries/source) or commit hash (for repositories)
- `@TIMESTAMP` - Replaced with current unix timestamp (seconds)
- `@ASSET_EXTENSION` - Replaced with file extension (only when extract=false)
- `$ENV_VAR` - Replaced with environment variable values

**Examples:**

```json
{
  "shadowsocks": {
    "path": "bin/ss/@VERSION/$DEPLOYMENT_ENV/",
    "source": "https://github.com/shadowsocks/shadowsocks-rust.git",
    "type": "binary",
    "asset_suffix": "aarch64-unknown-linux-gnu",
    "extract": true
  },
  "terraform": {
    "path": "tools/terraform-@VERSION",
    "source": "https://github.com/hashicorp/terraform.git",
    "type": "binary",
    "asset_suffix": "linux_amd64"
  },
  "my_library": {
    "path": "libs/$PROJECT_NAME/@VERSION/",
    "source": "https://github.com/owner/library.git",
    "type": "repository"
  },
  "timestamped_backup": {
    "path": "backups/@TIMESTAMP",
    "source": "https://github.com/owner/project.git",
    "type": "source",
    "extract": false,
    "filename": "backup-@VERSION-@TIMESTAMP.@ASSET_EXTENSION"
  }
}
```

**Usage with environment variables:**
```bash
# Set environment variables
export DEPLOYMENT_ENV=production
export PROJECT_NAME=myapp

# Install dependencies - paths will be expanded automatically
./glitch_deps install

# Results in paths like:
# bin/ss/v1.15.3/production/
# tools/terraform-v1.6.0
# libs/myapp/a1b2c3d4/
# backups/1703123456/backup-v1.2.3-1703123456.tar.gz
```

**Benefits:**
- **Version isolation**: Keep multiple versions side by side
- **Environment separation**: Different paths for dev/staging/production
- **Dynamic organization**: Organize dependencies based on runtime context
- **Timestamped backups**: Create unique timestamped archives
- **Lock file tracking**: Expanded paths are stored in lock files for consistency

### Custom Config Files

You can specify a custom configuration file using the `-c` flag:

```bash
# Use custom config file
./glitch_deps install -c my_deps.json
./glitch_deps update -c production_deps.json

# Different configs for different environments
./glitch_deps install -c dev_deps.json      # Development dependencies
./glitch_deps install -c prod_deps.json     # Production dependencies
./glitch_deps install -c test_deps.json     # Test dependencies
```

**Lock Files**: Each config file automatically gets its own lock file:
- `GLITCH_DEPS.json` → `GLITCH_DEPS-lock.json`
- `my_deps.json` → `my_deps-lock.json`
- `production_deps.json` → `production_deps-lock.json`

This allows you to maintain separate dependency versions for different environments or projects.

### Dependency Types

- **`binary`**: Downloads binary files from GitHub releases
- **`source`**: Downloads source code archives from GitHub releases
- **`repository`**: Clones Git repositories

### Source Code Dependencies

The `source` type allows you to download GitHub's automatically generated source code archives for any release:

```json
{
  "project_source": {
    "path": "sources/project",
    "source": "https://github.com/owner/project.git",
    "type": "source",
    "asset_extension": "tar.gz",
    "extract": true
  },
  "archived_source": {
    "path": "archives",
    "source": "https://github.com/owner/project.git",
    "type": "source",
    "asset_extension": "zip",
    "extract": false,
    "filename": "project-@VERSION-@TIMESTAMP.@ASSET_EXTENSION"
  }
}
```

**Source type configuration:**
- **`asset_extension`** (optional): `"zip"` or `"tar.gz"` (default: `"tar.gz"`)
- **`extract`** (optional): Extract archive contents (default: `false`)
- **`filename`** (optional): Custom archive filename (only when `extract=false`)

**Restrictions for source type:**
- ❌ `asset_name` - Not allowed (source archives have fixed names)
- ❌ `asset_suffix` - Not allowed (source archives have fixed names)
- ❌ `filename` with `extract=true` - Cannot specify filename when extracting
- ❌ `@ASSET_EXTENSION` with `extract=true` - Extension placeholder not meaningful when extracting

**Source code URLs:**
- ZIP: `https://github.com/owner/repo/archive/refs/tags/v1.0.0.zip`
- TAR.GZ: `https://github.com/owner/repo/archive/refs/tags/v1.0.0.tar.gz`

**Extraction behavior:**
- **`extract=true`**: Extracts source code to the specified directory, removing the top-level folder
- **`extract=false`**: Downloads the archive file with optional custom filename

### Asset Suffix Specification

For binary dependencies, you can specify the target asset suffix using the `asset_suffix` field. The package manager will search for assets containing this substring in their filename:

```json
{
  "cross_platform_tool": {
    "path": "bin/tool",
    "source": "https://github.com/owner/tool.git",
    "type": "binary",
    "asset_suffix": "linux_amd64"
  }
}
```

**Supported formats**: Any substring that appears in the asset filename:
- **Go style**: `linux_amd64`, `windows_amd64`, `darwin_arm64`
- **Rust target triples**: `x86_64-unknown-linux-gnu`, `aarch64-apple-darwin`
- **Node.js style**: `linux-x64`, `win32-x64`, `darwin-arm64`
- **Custom formats**: `ubuntu-20.04`, `static`, `musl`, etc.

**⚠️ Important**: `asset_suffix` is **required** for all binary dependencies. If not specified, the installation will fail with an error listing available assets.

### Archive Extraction

For binary dependencies that are distributed as archives, you can enable automatic extraction using the `extract` field:

```json
{
  "shadowsocks_all": {
    "path": "bin/shadowsocks",
    "source": "https://github.com/shadowsocks/shadowsocks-rust.git",
    "type": "binary",
    "asset_suffix": "aarch64-unknown-linux-gnu",
    "extract": true
  },
  "single_file_tool": {
    "path": "bin",
    "source": "https://github.com/owner/single-binary-release.git",
    "type": "binary",
    "asset_suffix": "linux_amd64",
    "extract": true,
    "filename": "my-tool"
  }
}
```

**Supported archive formats**:
- `.tar.gz` - Gzip compressed tar archives
- `.tar.xz` - XZ compressed tar archives
- `.zip` - ZIP archives

**Asset Selection Logic**:

The tool uses a strict three-stage filtering process to select the correct asset:

1. **First stage - `asset_name` filtering** (optional):
   ```json
   {
     "asset_name": "ubuntu-22.04"
   }
   ```
   - If specified, only assets containing this string in their name are considered
   - If no assets match, returns an error

2. **Second stage - `asset_extension` filtering** (optional):
   ```json
   {
     "asset_extension": "tar.gz"
   }
   ```
   - If specified, only assets ending with this file extension are considered
   - Automatically adds leading dot if not provided (e.g., `tar.gz` becomes `.tar.gz`)
   - Must match at the end of the filename (not anywhere in the middle)
   - If no assets match, returns an error

3. **Third stage - `asset_suffix` filtering** (required for binary):
   ```json
   {
     "asset_suffix": "linux_amd64"
   }
   ```
   - **Required for all binary dependencies**
   - Filters remaining assets by this suffix
   - If multiple assets match, returns an error with the list of matching assets
   - If no assets match, returns an error

**Examples of precise asset selection**:
```json
{
  "precise_tool": {
    "path": "bin/tool",
    "source": "https://github.com/owner/tool.git",
    "type": "binary",
    "asset_name": "ubuntu-22.04",
    "asset_extension": "tar.gz",
    "asset_suffix": "x86_64"
  }
}
```

This will:
1. Find all assets containing "ubuntu-22.04" in the name
2. Among those, find assets with ".tar.gz" extension
3. Among those, find assets containing "x86_64"
4. If exactly one asset matches all criteria, download it
5. If zero or multiple assets match, return an error

**Extraction Logic**:

When `extract` is set to `true`, the behavior depends on the `filename` field:

1. **Without `filename` field** - Extract all files to directory:
   ```json
   {
     "path": "bin/shadowsocks",
     "extract": true
   }
   ```
   - All files from the archive are extracted to the `path` directory
   - Directory structure is preserved
   - Works with any number of files in the archive

2. **With `filename` field** - Extract single file with specific name:
   ```json
   {
     "path": "bin",
     "extract": true,
     "filename": "my-tool"
   }
   ```
   - Archive **must contain exactly 1 file** (excluding directories)
   - The single file is extracted and renamed to `filename`
   - Final path becomes `path/filename` (e.g., `bin/my-tool`)
   - If archive contains 0 files → **Error: "no files found in archive"**
   - If archive contains 2+ files → **Error: "filename specified but archive contains X files (expected 1). Remove filename to extract all files to directory"**

**Error Handling**:
- If `filename` is specified but archive contains multiple files → **Error with count and helpful message**
- If `filename` is specified but archive contains no files → **Error**
- If `filename` is not specified → Extract all files regardless of count
- If no `asset_suffix` is specified for binary type → **Error** (no random asset downloads)
- If multiple assets match the criteria → **Error with list of matching assets**
- If no assets match `asset_name`, `asset_extension`, or `asset_suffix` → **Error with available options**

**Examples**:
```json
{
  "amneziawg_tools": {
    "path": "bin/amneziawg",
    "source": "https://github.com/amnezia-vpn/amneziawg-tools.git",
    "type": "binary",
    "asset_name": "ubuntu-22.04",
    "asset_extension": "tar.gz",
    "asset_suffix": "amneziawg-tools",
    "extract": true
  },
  "single_binary": {
    "path": "bin",
    "source": "https://github.com/owner/single-file-tool.git", 
    "type": "binary",
    "asset_suffix": "linux_amd64",
    "extract": true,
    "filename": "tool"
  }
}
```

**Process**:
1. Archive is downloaded to temporary directory (`./tmp`)
2. Archive is extracted based on the logic above
3. Files are moved to final destination
4. Temporary files are cleaned up
5. Only tool-created temporary files are removed, user files in `./tmp` remain untouched

If `extract` is not specified or set to `false`, the file is downloaded as-is without extraction.

### Private Repositories

Set the `GLITCH_DEPS_GITHUB_PAT` environment variable with your GitHub Personal Access Token:

```bash
export GLITCH_DEPS_GITHUB_PAT=ghp_xxxxxxxxxxxxxxxxxxxx
```

Mark private dependencies in your config:

```json
{
  "private_tool": {
    "path": "tools/private_tool", 
    "source": "https://github.com/private-org/tool.git",
    "type": "binary",
    "asset_suffix": "linux_amd64",
    "private": true
  },
  "private_source": {
    "path": "sources/private_project",
    "source": "https://github.com/private-org/project.git",
    "type": "source",
    "private": true,
    "extract": true
  }
}
```

## Commands

```bash
# Install all dependencies (default config)
glitch_deps install

# Install with custom config
glitch_deps install -c my_deps.json

# Update all dependencies  
glitch_deps update

# Update with custom config
glitch_deps update -c production_deps.json

# Update specific dependency
glitch_deps update my_provider

# Update specific dependency with custom config
glitch_deps update my_provider -c dev_deps.json

# Update to specific version
glitch_deps update my_provider v1.2.0

# Update glitch_deps itself
glitch_deps self-update

# Show help
glitch_deps help
```

## Use Cases

### Multi-Environment Setup

```bash
# Development environment
./glitch_deps install -c dev_deps.json

# Production environment  
./glitch_deps install -c prod_deps.json

# CI/CD environment
./glitch_deps install -c ci_deps.json
```

### Project-Specific Dependencies

```bash
# Frontend project dependencies
./glitch_deps install -c frontend_deps.json

# Backend project dependencies
./glitch_deps install -c backend_deps.json

# Infrastructure tools
./glitch_deps install -c infra_deps.json
```

## How It Works

- **Binary dependencies**: Downloads assets from GitHub releases. `asset_suffix` is **required** - if not specified, installation fails with error listing available assets
- **Source dependencies**: Downloads GitHub's automatically generated source code archives (ZIP/TAR.GZ) for any release
- **Repository dependencies**: Clones or pulls the latest changes from Git repositories  
- **Archive extraction**: Supports `.tar.gz`, `.tar.xz`, and `.zip` formats with intelligent single-file vs multi-file handling
- **Version tracking**: Creates corresponding lock files (e.g., `my_deps-lock.json`) to track installed versions and hashes
- **Smart updates**: Detects when updates are available and notifies you
- **Safe cleanup**: Only removes temporary files created by the tool, preserving user files in `./tmp`
- **Path substitutions**: Dynamic path expansion with version, timestamp, extension, and environment variables

## Requirements

- Git (must be in PATH)
- Internet access for GitHub API
- GitHub Personal Access Token (for private repositories only)

## License

Apache 2.0 