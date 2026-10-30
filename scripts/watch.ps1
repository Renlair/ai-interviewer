# 监控远端仓库，有更新就自动拉取。用法：powershell -ExecutionPolicy Bypass -File scripts\watch.ps1 [间隔秒数，默认 30]
param([int]$Interval = 30)
Set-Location (Join-Path $PSScriptRoot "..")
Write-Host "监控 $(git rev-parse --abbrev-ref HEAD) 分支，每 $Interval 秒检查一次，Ctrl+C 退出"
while ($true) {
  git fetch -q
  if ($LASTEXITCODE -eq 0 -and (git rev-parse HEAD) -ne (git rev-parse '@{u}')) {
    Write-Host "[$(Get-Date -Format HH:mm:ss)] 发现更新："
    git log --oneline 'HEAD..@{u}'
    # 只做快进合并：本地有冲突的改动时不会覆盖，只提示
    git pull -q --ff-only
    if ($LASTEXITCODE -eq 0) { Write-Host "已拉取，重新运行项目即可生效" } else { Write-Host "拉取失败，请先处理本地改动" }
  }
  Start-Sleep -Seconds $Interval
}
