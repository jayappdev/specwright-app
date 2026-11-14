<#
.SYNOPSIS
  Builds SpecWright for Windows: build\bin\SpecWright.exe, and optionally an
  installer and a code signature.

.DESCRIPTION
  Run it on Windows from any folder (PowerShell 5.1 or 7):

    scripts\build-windows.ps1                  build SpecWright.exe
    scripts\build-windows.ps1 -Installer       also build the NSIS installer
    scripts\build-windows.ps1 -Arch arm64      for Windows on ARM
    scripts\build-windows.ps1 -SignOnly        sign the existing build only
    scripts\build-windows.cmd ...              the same, from cmd or a double-click

  Needs Go, Node.js (npm) and the Wails CLI; -Installer also needs NSIS
  (makensis). The script checks for them and says how to install what is
  missing. The app needs the WebView2 runtime, which comes with Windows 10
  and 11; the installer adds it where it is missing.

  Signing (optional) stops SmartScreen from warning about an unknown
  publisher once the certificate has a reputation. Credential Manager does
  not depend on it: secrets saved there are found by every build. The
  certificate, first match wins:
    -CertThumbprint / $env:SPECWRIGHT_SIGN_CERT   a certificate in your store
                                                 (Cert:\CurrentUser\My), by thumbprint
    -PfxPath / $env:SPECWRIGHT_SIGN_PFX           a .pfx file; its password from
                                                 $env:SPECWRIGHT_SIGN_PFX_PASSWORD.
                                                 signtool takes it as an argument, so
                                                 other programs could see it while it
                                                 signs: prefer a certificate in your store
  Without one, the build stays unsigned and the script says so. Signing uses
  signtool.exe from the Windows SDK, with an RFC 3161 timestamp
  ($env:SPECWRIGHT_TIMESTAMP_URL, default DigiCert's public server) so the
  signature stays valid after the certificate expires.
#>
[CmdletBinding()]
param(
  [ValidateSet("amd64", "arm64")] [string] $Arch = "amd64",
  [switch] $Installer,
  [switch] $SignOnly,
  [string] $CertThumbprint = $env:SPECWRIGHT_SIGN_CERT,
  [string] $PfxPath = $env:SPECWRIGHT_SIGN_PFX
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root
$bin = Join-Path $root "build\bin"
$exe = Join-Path $bin "SpecWright.exe"

function Need([string] $cmd, [string] $how) {
  if (-not (Get-Command $cmd -ErrorAction SilentlyContinue)) {
    throw "${cmd} was not found. ${how}"
  }
}

# Go installs tools into %USERPROFILE%\go\bin, which a new shell may not have on PATH yet
$goBin = Join-Path $env:USERPROFILE "go\bin"
if ((Test-Path $goBin) -and -not (($env:PATH -split ";") -contains $goBin)) { $env:PATH = "$goBin;$env:PATH" }

if (-not $SignOnly) {
  Need "go" "Install Go from https://go.dev/dl/ and open a new terminal."
  Need "npm" "Install Node.js (LTS) from https://nodejs.org/ and open a new terminal."
  $wailsVersion = (Select-String -Path (Join-Path $root "go.mod") -Pattern "github.com/wailsapp/wails/v2 (v\S+)").Matches[0].Groups[1].Value
  Need "wails" "Install it with: go install github.com/wailsapp/wails/v2/cmd/wails@$wailsVersion"
  if ($Installer) { Need "makensis" "Install NSIS from https://nsis.sourceforge.io/ (or: winget install NSIS.NSIS) and add it to PATH." }

  $wailsArgs = @("build", "-clean", "-platform", "windows/$Arch")
  if ($Installer) { $wailsArgs += "-nsis" }
  Write-Host "> wails $($wailsArgs -join ' ')"
  & wails @wailsArgs
  if ($LASTEXITCODE -ne 0) { throw "wails build failed (exit $LASTEXITCODE)" }
  # the build empties frontend\dist; the placeholder is tracked so a fresh clone compiles
  $keep = Join-Path $root "frontend\dist\.gitkeep"
  if (-not (Test-Path $keep)) { New-Item -ItemType File -Path $keep -Force | Out-Null }
}
if (-not (Test-Path $exe)) { throw "no ${exe}: build first" }

# --- signing -----------------------------------------------------------------
$targets = @($exe) + @(Get-ChildItem -Path $bin -Filter "*installer*.exe" -ErrorAction SilentlyContinue | ForEach-Object FullName)
$signArgs = $null
if ($CertThumbprint) {
  $thumb = ($CertThumbprint -replace "\s", "").ToUpperInvariant()
  if (-not (Get-ChildItem Cert:\CurrentUser\My | Where-Object { $_.Thumbprint -eq $thumb -and $_.HasPrivateKey })) {
    throw "no certificate with thumbprint ${thumb} and a private key in Cert:\CurrentUser\My"
  }
  $signArgs = @("/sha1", $thumb)
} elseif ($PfxPath) {
  if (-not (Test-Path $PfxPath)) { throw "the .pfx file ${PfxPath} was not found" }
  $signArgs = @("/f", (Resolve-Path $PfxPath).Path)
  if ($env:SPECWRIGHT_SIGN_PFX_PASSWORD) { $signArgs += @("/p", $env:SPECWRIGHT_SIGN_PFX_PASSWORD) }
}

if (-not $signArgs) {
  Write-Warning "No code-signing certificate given, so the app is unsigned. Windows SmartScreen may warn when it is opened from a download. Secrets in Credential Manager work either way."
} else {
  $signtool = Get-Command signtool.exe -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Source
  if (-not $signtool) { # the Windows SDK puts it under Windows Kits, by version
    $kits = Join-Path ${env:ProgramFiles(x86)} "Windows Kits\10\bin"
    $signtool = Get-ChildItem -Path $kits -Recurse -Filter signtool.exe -ErrorAction SilentlyContinue |
      Where-Object { $_.FullName -match "\\x64\\" } | Sort-Object FullName -Descending | Select-Object -First 1 -ExpandProperty FullName
  }
  if (-not $signtool) { throw "signtool.exe was not found: install the Windows SDK (Signing Tools for Desktop Apps)" }
  $ts = if ($env:SPECWRIGHT_TIMESTAMP_URL) { $env:SPECWRIGHT_TIMESTAMP_URL } else { "http://timestamp.digicert.com" }
  foreach ($t in $targets) {
    & $signtool sign @signArgs /fd SHA256 /tr $ts /td SHA256 /d "SpecWright" $t
    if ($LASTEXITCODE -ne 0) { throw "signing ${t} failed (exit $LASTEXITCODE)" }
    & $signtool verify /pa /q $t
    if ($LASTEXITCODE -ne 0) { Write-Warning "${t} is signed, but Windows does not trust the certificate yet (a self-signed one, for example)." }
    Write-Host "signed ${t}"
  }
}

Write-Host ""
foreach ($t in $targets) { Write-Host ("built {0} ({1:N1} MB)" -f $t, ((Get-Item $t).Length / 1MB)) }
