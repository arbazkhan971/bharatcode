<#
.SYNOPSIS
  BharatCode installer for Windows.

.DESCRIPTION
  Downloads the prebuilt bharatcode.exe for this architecture from the latest
  GitHub release (or a specific -Version) and installs it to a bin directory,
  adding that directory to the user PATH if needed. No build toolchain required.

.EXAMPLE
  irm https://raw.githubusercontent.com/arbazkhan971/bharatcode/main/install.ps1 | iex

.EXAMPLE
  # Specific version / directory:
  $env:BHARATCODE_VERSION = "v0.2.0"; irm https://raw.githubusercontent.com/arbazkhan971/bharatcode/main/install.ps1 | iex
#>
param(
  [string]$Version = $env:BHARATCODE_VERSION,
  [string]$InstallDir = $(if ($env:BHARATCODE_INSTALL_DIR) { $env:BHARATCODE_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\bharatcode" })
)

$ErrorActionPreference = "Stop"
$Repo = "arbazkhan971/bharatcode"
$Binary = "bharatcode.exe"

function Fail($msg) { Write-Error "bharatcode-install: $msg"; exit 1 }

# Map architecture to the GoReleaser asset tokens.
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "x86" -and $env:PROCESSOR_ARCHITEW6432) {
  $env:PROCESSOR_ARCHITEW6432
} else {
  $env:PROCESSOR_ARCHITECTURE
}
switch ($arch) {
  "AMD64" { $ArchToken = "x86_64" }
  "ARM64" { $ArchToken = "arm64" }
  default { Fail "unsupported architecture: $arch" }
}

# Resolve the latest tag if none was requested.
if (-not $Version) {
  try {
    $rel = Invoke-RestMethod -UseBasicParsing -Uri "https://api.github.com/repos/$Repo/releases/latest" `
      -Headers @{ "User-Agent" = "bharatcode-install" }
    $Version = $rel.tag_name
  } catch { Fail "could not determine latest release; set `$env:BHARATCODE_VERSION" }
}
if (-not $Version) { Fail "no release version resolved" }

$Asset = "bharatcode_Windows_$ArchToken.zip"
$Url = "https://github.com/$Repo/releases/download/$Version/$Asset"

$tmp = Join-Path $env:TEMP ("bharatcode-" + [System.Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
try {
  $zip = Join-Path $tmp $Asset
  Write-Host "Downloading bharatcode $Version (Windows/$ArchToken)..."
  Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $zip
  Expand-Archive -Path $zip -DestinationPath $tmp -Force

  $exe = Join-Path $tmp $Binary
  if (-not (Test-Path $exe)) { Fail "archive did not contain $Binary" }

  New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
  Copy-Item -Path $exe -Destination (Join-Path $InstallDir $Binary) -Force
  Write-Host "Installed $Binary to $InstallDir"

  # Add InstallDir to the user PATH if it's not already there.
  $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
  if ($userPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$InstallDir", "User")
    Write-Host "Added $InstallDir to your user PATH. Open a new terminal to use 'bharatcode'."
  }
  & (Join-Path $InstallDir $Binary) version
  if ($LASTEXITCODE -ne 0) { Fail "installed binary failed validation" }
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
