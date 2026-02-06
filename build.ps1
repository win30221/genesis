# Genesis Build Script
# Cross-platform Go build tool

param(
    [string]$Platform = ""
)

# Define supported platforms
$platforms = @{
    "1" = @{ Name = "Windows (amd64)"; GOOS = "windows"; GOARCH = "amd64"; Ext = ".exe" }
    "2" = @{ Name = "Windows (386)"; GOOS = "windows"; GOARCH = "386"; Ext = ".exe" }
    "3" = @{ Name = "Linux (amd64)"; GOOS = "linux"; GOARCH = "amd64"; Ext = "" }
    "4" = @{ Name = "Linux (arm64)"; GOOS = "linux"; GOARCH = "arm64"; Ext = "" }
    "5" = @{ Name = "macOS (amd64)"; GOOS = "darwin"; GOARCH = "amd64"; Ext = "" }
    "6" = @{ Name = "macOS (arm64)"; GOOS = "darwin"; GOARCH = "arm64"; Ext = "" }
}

# Show menu
function Show-Menu {
    Write-Host "================================" -ForegroundColor Cyan
    Write-Host "  Genesis Build Script" -ForegroundColor Cyan
    Write-Host "================================" -ForegroundColor Cyan
    Write-Host ""
    Write-Host "Select target platform:" -ForegroundColor Yellow
    Write-Host ""
    
    foreach ($key in $platforms.Keys | Sort-Object) {
        $platform = $platforms[$key]
        Write-Host "  [$key] $($platform.Name)" -ForegroundColor Green
    }
    
    Write-Host ""
    Write-Host "  [0] Build All" -ForegroundColor Magenta
    Write-Host "  [Q] Quit" -ForegroundColor Red
    Write-Host ""
}

# Build binary
function Build-Binary {
    param(
        [string]$GOOS,
        [string]$GOARCH,
        [string]$Ext,
        [string]$Name
    )
    
    $outputName = "genesis-$GOOS-$GOARCH$Ext"
    
    Write-Host "Building: $Name" -ForegroundColor Cyan
    Write-Host "  Output: $outputName" -ForegroundColor Gray
    
    $env:GOOS = $GOOS
    $env:GOARCH = $GOARCH
    
    try {
        $output = & go build -o $outputName -ldflags="-s -w" main.go 2>&1
        
        if ($LASTEXITCODE -eq 0) {
            if (Test-Path $outputName) {
                $fileSize = (Get-Item $outputName).Length / 1MB
                Write-Host "  Success! (Size: $([math]::Round($fileSize, 2)) MB)" -ForegroundColor Green
            } else {
                Write-Host "  Failed: Output file not found" -ForegroundColor Red
            }
        } else {
            Write-Host "  Build failed:" -ForegroundColor Red
            Write-Host $output -ForegroundColor Red
        }
    } catch {
        Write-Host "  Error: $_" -ForegroundColor Red
    }
    
    Write-Host ""
}

# Main function
function Main {
    # Check if in correct directory
    if (-not (Test-Path "main.go")) {
        Write-Host "Error: main.go not found" -ForegroundColor Red
        Write-Host "Please run this script from the project root directory" -ForegroundColor Yellow
        exit 1
    }
    
    # Check if Go is installed
    try {
        $goVersion = & go version 2>&1
        Write-Host "Go version: $goVersion" -ForegroundColor Gray
        Write-Host ""
    } catch {
        Write-Host "Error: Go compiler not found" -ForegroundColor Red
        Write-Host "Please install Go: https://golang.org/dl/" -ForegroundColor Yellow
        exit 1
    }
    
    # Show menu if no parameter provided
    if ([string]::IsNullOrEmpty($Platform)) {
        Show-Menu
        $choice = Read-Host "Your choice"
    } else {
        $choice = $Platform
    }
    
    # Process selection
    switch ($choice.ToUpper()) {
        "Q" {
            Write-Host "Cancelled" -ForegroundColor Yellow
            exit 0
        }
        "0" {
            Write-Host "Building all platforms..." -ForegroundColor Cyan
            Write-Host ""
            foreach ($key in $platforms.Keys | Sort-Object) {
                $p = $platforms[$key]
                Build-Binary -GOOS $p.GOOS -GOARCH $p.GOARCH -Ext $p.Ext -Name $p.Name
            }
            Write-Host "All builds completed!" -ForegroundColor Green
        }
        default {
            if ($platforms.ContainsKey($choice)) {
                $p = $platforms[$choice]
                Build-Binary -GOOS $p.GOOS -GOARCH $p.GOARCH -Ext $p.Ext -Name $p.Name
                Write-Host "Build completed!" -ForegroundColor Green
            } else {
                Write-Host "Invalid choice: $choice" -ForegroundColor Red
                exit 1
            }
        }
    }
}

# Run main function
Main
