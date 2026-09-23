# 生成 Gleam 应用图标 assets/gleam.ico（多尺寸 PNG 压缩 ICO，Vista+ 支持）
Add-Type -AssemblyName System.Drawing

function New-IconPng([int]$size) {
    $bmp = New-Object System.Drawing.Bitmap($size, $size)
    $g = [System.Drawing.Graphics]::FromImage($bmp)
    $g.SmoothingMode = 'AntiAlias'
    # 深色渐变背景
    $rect = New-Object System.Drawing.Rectangle(0, 0, $size, $size)
    $bg = New-Object System.Drawing.Drawing2D.LinearGradientBrush($rect, `
        [System.Drawing.Color]::FromArgb(255, 20, 26, 42), `
        [System.Drawing.Color]::FromArgb(255, 10, 14, 24), 90)
    $g.FillRectangle($bg, $rect)
    # 中心光点 + 光晕
    $cx = $size * 0.5; $cy = $size * 0.42; $r1 = $size * 0.20
    $haloPath = New-Object System.Drawing.Drawing2D.GraphicsPath
    $haloPath.AddEllipse(($cx - $r1 * 2.2), ($cy - $r1 * 2.2), ($r1 * 4.4), ($r1 * 4.4))
    $pgb = New-Object System.Drawing.Drawing2D.PathGradientBrush($haloPath)
    $pgb.CenterColor = [System.Drawing.Color]::FromArgb(210, 150, 235, 195)
    $pgb.SurroundColors = @([System.Drawing.Color]::FromArgb(0, 150, 235, 195))
    $g.FillPath($pgb, $haloPath)
    $core = New-Object System.Drawing.SolidBrush([System.Drawing.Color]::FromArgb(255, 240, 255, 248))
    $g.FillEllipse($core, ($cx - $r1), ($cy - $r1), ($r1 * 2), ($r1 * 2))
    # 八向光线
    $penW = [Math]::Max(1.0, $size * 0.035)
    $rayPen = New-Object System.Drawing.Pen([System.Drawing.Color]::FromArgb(235, 175, 242, 205), [single]$penW)
    $rayPen.StartCap = 'Round'; $rayPen.EndCap = 'Round'
    foreach ($a in @(0, 45, 90, 135, 180, 225, 270, 315)) {
        $rad = $a * [Math]::PI / 180
        $inner = $r1 * 1.45; $outer = $r1 * 2.15
        $g.DrawLine($rayPen, `
            [single]($cx + [Math]::Cos($rad) * $inner), [single]($cy + [Math]::Sin($rad) * $inner), `
            [single]($cx + [Math]::Cos($rad) * $outer), [single]($cy + [Math]::Sin($rad) * $outer))
    }
    $g.Dispose()
    $ms = New-Object System.IO.MemoryStream
    $bmp.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
    $bmp.Dispose()
    return $ms.ToArray()
}

$sizes = @(256, 64, 48, 32, 16)
$entries = @()
foreach ($s in $sizes) { $entries += , @{ Size = $s; Bytes = (New-IconPng $s) } }

New-Item -ItemType Directory -Force -Path assets | Out-Null
$fs = [System.IO.File]::Create("$PSScriptRoot\..\assets\gleam.ico")
$bw = New-Object System.IO.BinaryWriter($fs)
# ICONDIR
$bw.Write([uint16]0); $bw.Write([uint16]1); $bw.Write([uint16]$entries.Count)
# ICONDIRENTRY（256 编码为 0）
$offset = 6 + 16 * $entries.Count
foreach ($e in $entries) {
    $dim = if ($e.Size -ge 256) { 0 } else { $e.Size }
    $bw.Write([byte]$dim); $bw.Write([byte]$dim)
    $bw.Write([byte]0); $bw.Write([byte]0)
    $bw.Write([uint16]1); $bw.Write([uint16]32)
    $bw.Write([uint32]$e.Bytes.Length)
    $bw.Write([uint32]$offset)
    $offset += $e.Bytes.Length
}
# 图像数据
foreach ($e in $entries) { $bw.Write($e.Bytes) }
$bw.Flush(); $bw.Close()
Write-Host "OK: $((Get-Item "$PSScriptRoot\..\assets\gleam.ico").Length) bytes"
