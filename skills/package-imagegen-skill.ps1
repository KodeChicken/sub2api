$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$source = Join-Path $PSScriptRoot 'sub2api-imagegen'
$target = Join-Path $root 'frontend/public/downloads/sub2api-imagegen.zip'
$stage = Join-Path ([IO.Path]::GetTempPath()) ('sub2api-imagegen-' + [guid]::NewGuid().ToString('N'))
try {
    $bundle = Join-Path $stage 'sub2api-imagegen'
    New-Item -ItemType Directory -Path (Join-Path $bundle 'scripts'), (Join-Path $bundle 'references'), (Join-Path $bundle 'agents') -Force | Out-Null
    Copy-Item (Join-Path $source 'SKILL.md') $bundle
    Copy-Item (Join-Path $source 'scripts/generate_image.py') (Join-Path $bundle 'scripts')
    Copy-Item (Join-Path $source 'references/api.md') (Join-Path $bundle 'references')
    Copy-Item (Join-Path $source 'agents/openai.yaml') (Join-Path $bundle 'agents')
    New-Item -ItemType Directory -Path (Split-Path $target) -Force | Out-Null
    Compress-Archive -Path $bundle -DestinationPath $target -Force
    Get-FileHash $target -Algorithm SHA256 | Select-Object Path, Hash
} finally {
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
    $resolvedStage = [IO.Path]::GetFullPath($stage)
    if ($resolvedStage.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase) -and
        (Split-Path $resolvedStage -Leaf) -like 'sub2api-imagegen-*' -and
        (Test-Path -LiteralPath $resolvedStage)) {
        Remove-Item -LiteralPath $resolvedStage -Recurse -Force
    }
}
