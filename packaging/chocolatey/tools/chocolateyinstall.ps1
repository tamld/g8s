# TEMPLATE (demand-gated -- not auto-published by .goreleaser.yaml).
# Replace $checksum below with the SHA-256 from release checksums.txt before publishing.
$toolsDir = "$(Split-Path -Parent $MyInvocation.MyCommand.Definition)"
$url = 'https://github.com/tamld/g8s/releases/download/v0.27.1/g8s_0.27.1_windows_amd64.zip'
$checksum = '<from checksums.txt>'
if ($checksum -eq '<from checksums.txt>') {
  throw 'Chocolatey template checksum placeholder must be replaced from release checksums.txt before install'
}
$packageArgs = @{
  packageName    = 'g8s'
  unzipLocation  = $toolsDir
  url            = $url
  checksum       = $checksum
  checksumType   = 'sha256'
}
Install-ChocolateyZipPackage @packageArgs
