param(
    [switch]$Installer,
    [switch]$Offline,
    [ValidateSet('amd64', 'arm64')]
    [string]$Architecture = 'amd64',
    [string]$Wails = 'wails3',
    [string]$Makensis = 'makensis'
)

$ErrorActionPreference = 'Stop'
# Invoke a native build step and stop before packaging a stale binary if it fails.
function Invoke-BuildTool {
    param([string]$Tool, [string[]]$ToolArgs)
    & $Tool @ToolArgs
    if ($LASTEXITCODE -ne 0) { throw "$Tool failed with exit code $LASTEXITCODE" }
}

$oddRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$oddOutput = Join-Path $oddRoot 'bin'
$oddStage = Join-Path $oddOutput "windows-$Architecture"
$oddAssets = Join-Path $oddStage 'build'
$oddVersion = '0.1.0'
$oddDesktop = Join-Path $oddOutput "Shengji-$Architecture.exe"
$oddServer = Join-Path $oddOutput "Shengji-server-$Architecture.exe"
if ($Offline -and $Architecture -ne 'amd64') { throw 'The offline installer is verified on Windows AMD64.' }
$oddFlavor = if ($Offline) { '-offline' } else { '' }
$oddPackage = Join-Path $oddOutput "Shengji-$Architecture$oddFlavor-installer.exe"
$oddResource = Join-Path $oddRoot "zz_oddminutes_windows_$Architecture.syso"
if (Test-Path -LiteralPath $oddResource) { throw "Temporary resource already exists: $oddResource" }
$oddPrevious = @{ CGO_ENABLED = $env:CGO_ENABLED; GOOS = $env:GOOS; GOARCH = $env:GOARCH; GOTOOLCHAIN = $env:GOTOOLCHAIN }
$env:CGO_ENABLED = '0'
$env:GOOS = 'windows'
$env:GOARCH = $Architecture
$env:GOTOOLCHAIN = 'go1.27.2'
Push-Location -LiteralPath $oddRoot
try {
    New-Item -ItemType Directory -Force -Path $oddOutput | Out-Null
    Invoke-BuildTool $Wails @('update', 'build-assets', '-dir', $oddAssets, '-name', 'Shengji',
        '-binaryname', 'Shengji', '-productname', '声记', '-productcompany', 'OddMeta',
        '-productidentifier', 'net.oddmeta.oddminutes.go', '-productversion', $oddVersion,
        '-productdescription', 'Local meeting recorder', '-productcopyright', 'OddMeta')
    Invoke-BuildTool $Wails @('generate', 'syso', '-arch', $Architecture,
        '-icon', (Join-Path $oddRoot 'web\favicon.ico'),
        '-manifest', (Join-Path $oddAssets 'windows\wails.exe.manifest'),
        '-info', (Join-Path $oddAssets 'windows\info.json'), '-out', $oddResource)
    Invoke-BuildTool 'go' @('build', '-tags', 'production', '-trimpath', '-ldflags', '-s -w -H windowsgui',
        '-o', $oddDesktop, '.')
    Remove-Item -LiteralPath $oddResource
    Invoke-BuildTool 'go' @('build', '-tags', 'server,production', '-trimpath', '-ldflags', '-s -w',
        '-o', $oddServer, '.')
    $oddExtraDefines = @()
    if ($Offline) {
        $oddSherpa = Join-Path $oddOutput 'sherpa'
        New-Item -ItemType Directory -Force -Path $oddSherpa | Out-Null
        $env:CGO_ENABLED = '1'
        Invoke-BuildTool 'go' @('build', '-tags', 'sherpa', '-trimpath', '-ldflags', '-s -w',
            '-o', (Join-Path $oddSherpa 'OddMinutes-sherpa.exe'), './cmd/sherpa-asr')
        $env:CGO_ENABLED = '0'
        $oddNativeModule = go mod download -json github.com/k2-fsa/sherpa-onnx-go-windows | ConvertFrom-Json
        if ($LASTEXITCODE -ne 0 -or !$oddNativeModule.Dir) { throw 'Sherpa-ONNX dependency was not resolved.' }
        $oddSherpaLib = Join-Path $oddNativeModule.Dir 'lib\x86_64-pc-windows-gnu'
        foreach ($oddDLL in @('onnxruntime.dll', 'sherpa-onnx-c-api.dll')) {
            Copy-Item -LiteralPath (Join-Path $oddSherpaLib $oddDLL) -Destination $oddSherpa -Force
        }
        Copy-Item -LiteralPath (Join-Path $oddNativeModule.Dir 'LICENSE') -Destination (Join-Path $oddSherpa 'Sherpa-ONNX-LICENSE.txt') -Force
        foreach ($oddNotice in @('ONNX-Runtime-LICENSE.txt','ONNX-Runtime-ThirdPartyNotices.txt')) {
            Copy-Item -LiteralPath (Join-Path $PSScriptRoot "licenses\$oddNotice") -Destination $oddSherpa -Force
        }
        $oddExtraDefines += "/DODDMINUTES_SHERPA=$oddSherpa"
    }
    if ($Installer) {
        $oddNSIS = Join-Path $oddAssets 'windows\nsis'
        Invoke-BuildTool $Wails @('generate', 'webview2bootstrapper', '-dir', $oddNSIS)
        Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'installer.nsi') -Destination (Join-Path $oddNSIS 'oddminutes.nsi')
        Copy-Item -LiteralPath (Join-Path $oddRoot 'web\favicon.ico') -Destination (Join-Path $oddNSIS 'oddminutes.ico')
        Push-Location -LiteralPath $oddNSIS
        try {
            Invoke-BuildTool $Makensis (@('/V2', '/INPUTCHARSET', 'UTF8', "/DARG_WAILS_$($Architecture.ToUpperInvariant())_BINARY=$oddDesktop",
                "/DODDMINUTES_INSTALLER=$oddPackage") + $oddExtraDefines + @('oddminutes.nsi'))
        } finally {
            Pop-Location
        }
        Get-Item -LiteralPath $oddPackage
    }
    Get-Item -LiteralPath $oddDesktop, $oddServer | Select-Object Name, Length, FullName
} finally {
    if (Test-Path -LiteralPath $oddResource) { Remove-Item -LiteralPath $oddResource }
    foreach ($oddName in $oddPrevious.Keys) {
        [Environment]::SetEnvironmentVariable($oddName, $oddPrevious[$oddName], 'Process')
    }
    Pop-Location
}
